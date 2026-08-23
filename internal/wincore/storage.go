package wincore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var (
	databaseSizeLimit int64 = 100 << 20
	retentionDays           = 7
	maxStoreSize     int64  = 1 << 30 // 1 GB
)

type Store struct {
	sync.Mutex
	db         *sql.DB
	dir        string
	path       string
	date       string
	index      int
	sessionID  int64
	mode       string
	endpoint   string
	parameters string
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	if err := s.openFileLocked(time.Now(), false); err != nil {
		return nil, err
	}
	s.cleanupOldFiles()
	return s, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) StartSession(mode, endpoint, parameters string) error {
	s.Lock()
	defer s.Unlock()
	s.finishSessionLocked(time.Now())
	s.mode, s.endpoint, s.parameters = mode, endpoint, parameters
	return s.insertSessionLocked(time.Now())
}

func (s *Store) EndSession() {
	s.Lock()
	defer s.Unlock()
	s.finishSessionLocked(time.Now())
	s.mode, s.endpoint, s.parameters = "", "", ""
}

// ponytail: synchronous inserts preserve every raw read; batch transactions if sustained throughput proves too slow.
func (s *Store) Received(source string, data []byte) error {
	s.Lock()
	defer s.Unlock()
	if s.db == nil || s.sessionID == 0 {
		return nil
	}
	now := time.Now()
	if err := s.rotateIfNeededLocked(now, int64(len(data))); err != nil {
		return err
	}
	validUTF8 := utf8.Valid(data)
	textData := string(data)
	if !validUTF8 {
		textData = strings.ToValidUTF8(textData, "�")
	}
	_, err := s.db.Exec(`INSERT INTO received_data(session_id, received_at, source, size_bytes, raw_data, text_data, is_utf8)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, s.sessionID, now.Format(time.RFC3339Nano), source, len(data), data, textData, validUTF8)
	return err
}

// SessionInfo 是一条历史连接记录。
type SessionInfo struct {
	Mode       string
	Endpoint   string
	Parameters string
	StartedAt  string
}

// RecentSessions 返回去重后最近使用的若干条连接配置(按最近使用时间倒序)。
func (s *Store) RecentSessions(limit int) ([]SessionInfo, error) {
	s.Lock()
	defer s.Unlock()
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT mode, endpoint, parameters, MAX(started_at)
		FROM sessions GROUP BY mode, endpoint, parameters ORDER BY MAX(id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		if err := rows.Scan(&si.Mode, &si.Endpoint, &si.Parameters, &si.StartedAt); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

func (s *Store) Close() {
	s.Lock()
	defer s.Unlock()
	s.finishSessionLocked(time.Now())
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
}

func (s *Store) rotateIfNeededLocked(now time.Time, incomingSize int64) error {
	date := now.Format("20060102")
	if date == s.date && databaseFamilySize(s.path)+incomingSize < databaseSizeLimit {
		return nil
	}
	active := s.sessionID != 0
	s.finishSessionLocked(now)
	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
	if err := s.openFileLocked(now, date == s.date); err != nil {
		return err
	}
	if active {
		return s.insertSessionLocked(now)
	}
	return nil
}

func (s *Store) openFileLocked(now time.Time, forceNew bool) error {
	date := now.Format("20060102")
	index := 1
	files, _ := filepath.Glob(filepath.Join(s.dir, "serial-data-"+date+"-*.sqlite3"))
	sort.Strings(files)
	if len(files) > 0 {
		last := files[len(files)-1]
		base := strings.TrimSuffix(filepath.Base(last), ".sqlite3")
		if parsed, err := strconv.Atoi(base[strings.LastIndex(base, "-")+1:]); err == nil {
			index = parsed
		}
		if forceNew || databaseFamilySize(last) >= databaseSizeLimit {
			index++
		}
	}
	path := filepath.Join(s.dir, fmt.Sprintf("serial-data-%s-%03d.sqlite3", date, index))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`
		PRAGMA busy_timeout=5000;
		PRAGMA journal_mode=WAL;
		PRAGMA synchronous=NORMAL;
		CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at TEXT NOT NULL,
			ended_at TEXT,
			mode TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			parameters TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS received_data (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id INTEGER NOT NULL,
			received_at TEXT NOT NULL,
			source TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			raw_data BLOB NOT NULL,
			text_data TEXT,
			is_utf8 INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY(session_id) REFERENCES sessions(id)
		);
		CREATE INDEX IF NOT EXISTS idx_received_time ON received_data(received_at);
		CREATE INDEX IF NOT EXISTS idx_received_session ON received_data(session_id);
	`); err != nil {
		_ = db.Close()
		return err
	}
	s.db, s.path, s.date, s.index = db, path, date, index
	return nil
}

func (s *Store) insertSessionLocked(now time.Time) error {
	id, err := s.insertSessionRow(now, s.mode, s.endpoint, s.parameters)
	if err != nil {
		return err
	}
	s.sessionID = id
	return nil
}

// insertSessionRow 插入一条会话记录并返回其 ID,不影响全局 sessionID。
func (s *Store) insertSessionRow(now time.Time, mode, endpoint, parameters string) (int64, error) {
	result, err := s.db.Exec(`INSERT INTO sessions(started_at, mode, endpoint, parameters) VALUES (?, ?, ?, ?)`,
		now.Format(time.RFC3339Nano), mode, endpoint, parameters)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// NewSession 为后台任务(如虚拟串口)插入独立会话并返回 ID,不影响主连接的全局会话。
func (s *Store) NewSession(mode, endpoint, parameters string) (int64, error) {
	s.Lock()
	defer s.Unlock()
	return s.insertSessionRow(time.Now(), mode, endpoint, parameters)
}

// EndSessionID 结束指定会话。
func (s *Store) EndSessionID(id int64) {
	s.Lock()
	defer s.Unlock()
	if s.db == nil || id == 0 {
		return
	}
	_, _ = s.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, time.Now().Format(time.RFC3339Nano), id)
}

// ReceivedForSession 把一条报文写入指定会话(用于后台任务如虚拟串口)。
func (s *Store) ReceivedForSession(sessionID int64, source string, data []byte) error {
	s.Lock()
	defer s.Unlock()
	if s.db == nil || sessionID == 0 {
		return nil
	}
	now := time.Now()
	if err := s.rotateIfNeededLocked(now, int64(len(data))); err != nil {
		return err
	}
	validUTF8 := utf8.Valid(data)
	textData := string(data)
	if !validUTF8 {
		textData = strings.ToValidUTF8(textData, "�")
	}
	_, err := s.db.Exec(`INSERT INTO received_data(session_id, received_at, source, size_bytes, raw_data, text_data, is_utf8)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, sessionID, now.Format(time.RFC3339Nano), source, len(data), data, textData, validUTF8)
	return err
}

func (s *Store) finishSessionLocked(now time.Time) {
	if s.db == nil || s.sessionID == 0 {
		return
	}
	_, _ = s.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), s.sessionID)
	s.sessionID = 0
}

func databaseFamilySize(path string) int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

// cleanupOldFiles 清理过期数据:删除超过 retentionDays 天的数据文件,
// 若总大小仍超过 maxStoreSize,再删除最旧文件直到不超限。
func (s *Store) cleanupOldFiles() {
	files, _ := filepath.Glob(filepath.Join(s.dir, "serial-data-*.sqlite3"))
	if len(files) == 0 {
		return
	}
	sort.Strings(files)
	cutoff := time.Now().AddDate(0, 0, -retentionDays).Format("20060102")

	var total int64
	keep := make([]string, 0, len(files))
	for _, f := range files {
		if date := fileDate(f); date != "" && date < cutoff {
			removeDBFile(f)
			continue
		}
		keep = append(keep, f)
		total += databaseFamilySize(f)
	}
	for i := 0; total > maxStoreSize && i < len(keep); i++ {
		total -= databaseFamilySize(keep[i])
		removeDBFile(keep[i])
	}
}

// fileDate 从文件名 serial-data-YYYYMMDD-NNN.sqlite3 提取日期 YYYYMMDD。
func fileDate(path string) string {
	rest := strings.TrimPrefix(filepath.Base(path), "serial-data-")
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		return rest[:i]
	}
	return ""
}

// removeDBFile 删除数据文件及其 -wal/-shm 附属文件。
func removeDBFile(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}

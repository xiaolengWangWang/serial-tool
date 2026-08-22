package main

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

	_ "github.com/mattn/go-sqlite3"
)

const defaultDatabaseSizeLimit = 100 << 20

var databaseSizeLimit int64 = defaultDatabaseSizeLimit

var storage struct {
	sync.Mutex
	db            *sql.DB
	dir           string
	path          string
	date          string
	index         int
	sessionID     int64
	mode          string
	endpoint      string
	parameters    string
	initErr       error
	errorReported bool
}

func openDatabase() error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		storage.initErr = err
		return err
	}
	storage.dir = filepath.Join(configDir, "GoSerialTool", "data")
	if err = os.MkdirAll(storage.dir, 0o755); err != nil {
		storage.initErr = err
		return err
	}
	storage.Lock()
	defer storage.Unlock()
	err = openDatabaseFileLocked(time.Now(), false)
	storage.initErr = err
	return err
}

func closeDatabase() {
	storage.Lock()
	defer storage.Unlock()
	finishSessionLocked(time.Now())
	if storage.db != nil {
		_ = storage.db.Close()
		storage.db = nil
	}
}

func databaseDirectory() (string, error) {
	storage.Lock()
	defer storage.Unlock()
	if storage.initErr != nil {
		return "", storage.initErr
	}
	return storage.dir, nil
}

func startStorageSession(mode, endpoint, parameters string) error {
	storage.Lock()
	defer storage.Unlock()
	if storage.db == nil {
		return storage.initErr
	}
	finishSessionLocked(time.Now())
	storage.mode, storage.endpoint, storage.parameters = mode, endpoint, parameters
	return insertSessionLocked(time.Now())
}

func endStorageSession() {
	storage.Lock()
	defer storage.Unlock()
	finishSessionLocked(time.Now())
	storage.mode, storage.endpoint, storage.parameters = "", "", ""
}

// ponytail: synchronous inserts preserve every raw read; batch transactions if sustained throughput proves too slow.
func storeReceived(source string, data []byte) error {
	storage.Lock()
	defer storage.Unlock()
	if storage.db == nil || storage.sessionID == 0 {
		return nil
	}
	now := time.Now()
	if err := rotateIfNeededLocked(now, int64(len(data))); err != nil {
		return reportStorageErrorLocked(err)
	}
	validUTF8 := utf8.Valid(data)
	textData := string(data)
	if !validUTF8 {
		textData = strings.ToValidUTF8(textData, "�")
	}
	_, err := storage.db.Exec(`INSERT INTO received_data(session_id, received_at, source, size_bytes, raw_data, text_data, is_utf8)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, storage.sessionID, now.Format(time.RFC3339Nano), source, len(data), data, textData, validUTF8)
	if err != nil {
		return reportStorageErrorLocked(err)
	}
	return nil
}

func reportStorageErrorLocked(err error) error {
	if storage.errorReported {
		return nil
	}
	storage.errorReported = true
	return err
}

func rotateIfNeededLocked(now time.Time, incomingSize int64) error {
	date := now.Format("20060102")
	size := databaseFamilySize(storage.path)
	if date == storage.date && size+incomingSize < databaseSizeLimit {
		return nil
	}
	active := storage.sessionID != 0
	finishSessionLocked(now)
	if storage.db != nil {
		_ = storage.db.Close()
		storage.db = nil
	}
	forceNew := date == storage.date
	if err := openDatabaseFileLocked(now, forceNew); err != nil {
		return err
	}
	if active {
		return insertSessionLocked(now)
	}
	return nil
}

func openDatabaseFileLocked(now time.Time, forceNew bool) error {
	date := now.Format("20060102")
	index := 1
	files, _ := filepath.Glob(filepath.Join(storage.dir, "serial-data-"+date+"-*.sqlite3"))
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
	path := filepath.Join(storage.dir, fmt.Sprintf("serial-data-%s-%03d.sqlite3", date, index))
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`
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
	for _, migration := range []string{
		`ALTER TABLE received_data ADD COLUMN text_data TEXT`,
		`ALTER TABLE received_data ADD COLUMN is_utf8 INTEGER NOT NULL DEFAULT 1`,
	} {
		if _, migrationErr := db.Exec(migration); migrationErr != nil && !strings.Contains(migrationErr.Error(), "duplicate column name") {
			_ = db.Close()
			return migrationErr
		}
	}
	storage.db, storage.path, storage.date, storage.index = db, path, date, index
	return nil
}

func insertSessionLocked(now time.Time) error {
	result, err := storage.db.Exec(`INSERT INTO sessions(started_at, mode, endpoint, parameters) VALUES (?, ?, ?, ?)`,
		now.Format(time.RFC3339Nano), storage.mode, storage.endpoint, storage.parameters)
	if err != nil {
		return err
	}
	storage.sessionID, err = result.LastInsertId()
	return err
}

func finishSessionLocked(now time.Time) {
	if storage.db == nil || storage.sessionID == 0 {
		return
	}
	_, _ = storage.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, now.Format(time.RFC3339Nano), storage.sessionID)
	storage.sessionID = 0
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

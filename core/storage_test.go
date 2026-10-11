package core

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreLargeMetadataForcesNextSizeCheck(t *testing.T) {
	oldLimit := databaseSizeLimit
	databaseSizeLimit = 2 << 20
	t.Cleanup(func() { databaseSizeLimit = oldLimit })
	for _, writer := range []string{"received", "packet", "background"} {
		t.Run(writer, func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.StartSession("main", "endpoint", ""); err != nil {
				t.Fatal(err)
			}
			handle, err := s.NewSession("background", "bridge", "")
			if err != nil {
				t.Fatal(err)
			}
			largeSource := strings.Repeat("x", 3<<20)
			switch writer {
			case "received":
				err = s.Received(largeSource, []byte("x"))
			case "packet":
				err = s.Record(Packet{Source: largeSource, Data: []byte("x")})
			case "background":
				err = s.ReceivedForSession(handle, largeSource, []byte("x"))
			}
			if err != nil {
				t.Fatal(err)
			}
			oldPath := s.path
			if err := s.Received("small", []byte("data")); err != nil {
				t.Fatal(err)
			}
			if s.path == oldPath {
				t.Fatal("large metadata growth was not checked before the next small record")
			}
		})
	}
}

// Every writer must keep raw bytes and session ownership across a date change.
func TestStoreWritersAcrossDateRotation(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.StartSession("main", "endpoint", ""); err != nil {
		t.Fatal(err)
	}
	handle, err := s.NewSession("background", "bridge", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte{0, 0xff, 1, 3}
	write := func() {
		t.Helper()
		for _, err := range []error{
			s.Received("received", raw),
			s.Record(Packet{Source: "packet", Data: raw, ID: "packet-id"}),
			s.ReceivedForSession(handle, "background", raw),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	write()
	oldPath := s.path
	// Exercise future and then current packet dates without sleeping until midnight.
	if err := s.Record(Packet{Source: "packet", Data: raw, ID: "next-day", Timestamp: time.Now().AddDate(0, 0, 1)}); err != nil {
		t.Fatal(err)
	}
	if s.path == oldPath {
		t.Fatal("date change did not rotate capture")
	}
	write()
	s.Close()
	files, err := filepath.Glob(filepath.Join(s.dir, "serial-data-*.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range files {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query(`SELECT r.source, r.raw_data, COALESCE(s.mode, '') FROM received_data r LEFT JOIN sessions s ON s.id=r.session_id`)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			var source, mode string
			var data []byte
			if err := rows.Scan(&source, &data, &mode); err != nil {
				t.Fatal(err)
			}
			wantMode := "main"
			if source == "background" {
				wantMode = "background"
			}
			if !bytes.Equal(data, raw) || mode != wantMode {
				t.Fatalf("source=%s data=%x mode=%s", source, data, mode)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		db.Close()
	}
	if count != 7 {
		t.Fatalf("record count = %d, want 7", count)
	}
}

func TestStoreDetectsDatabaseGrowthDuringCapture(t *testing.T) {
	oldLimit := databaseSizeLimit
	databaseSizeLimit = 16 << 20
	t.Cleanup(func() { databaseSizeLimit = oldLimit })
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.StartSession("main", "endpoint", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Received("warm", []byte("warm")); err != nil {
		t.Fatal(err)
	}
	oldPath := s.path
	// Grow the actual SQLite/WAL files outside the normal capture write path.
	if _, err := s.db.Exec(`INSERT INTO received_data(session_id, received_at, source, size_bytes, raw_data) VALUES (?, ?, 'growth', ?, zeroblob(?))`, s.sessionID, time.Now().Format(time.RFC3339Nano), 20<<20, 20<<20); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		if err := s.Received("capture", []byte("data")); err != nil {
			t.Fatal(err)
		}
	}
	if s.path == oldPath {
		t.Fatal("capture did not detect database growth within 32 writes")
	}
	s.Close()
	files, err := filepath.Glob(filepath.Join(s.dir, "serial-data-*.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range files {
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		err = db.QueryRow(`SELECT COUNT(*) FROM received_data WHERE source='capture' AND raw_data=x'64617461'`).Scan(&n)
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
		count += n
	}
	if count != 32 {
		t.Fatalf("capture records = %d, want 32", count)
	}
}

// 后台会话(虚拟串口)跨数据文件轮换后,数据仍须挂在自己的会话行上:
// 不能落到新文件里不存在的会话,也不能撞上之后新建的主连接会话。
func TestBackgroundSessionSurvivesRotation(t *testing.T) {
	old := databaseSizeLimit
	databaseSizeLimit = 64 << 10
	t.Cleanup(func() { databaseSizeLimit = old })
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartSession("主连接A", "a", ""); err != nil {
		t.Fatal(err)
	}
	handle, err := s.NewSession("虚拟串口", "127.0.0.1:9", "")
	if err != nil || handle == 0 {
		t.Fatalf("NewSession = %d, %v", handle, err)
	}
	if err := s.ReceivedForSession(handle, "vs", make([]byte, 80<<10)); err != nil { // 触发轮换
		t.Fatal(err)
	}
	if err := s.ReceivedForSession(handle, "vs", []byte("after")); err != nil {
		t.Fatal(err)
	}
	s.EndSession()
	if err := s.StartSession("主连接B", "b", ""); err != nil {
		t.Fatal(err)
	}
	s.EndSessionID(handle)
	if err := s.ReceivedForSession(handle, "vs", []byte("ended")); err != nil { // 已结束的句柄不再写
		t.Fatal(err)
	}
	s.Close()

	files, _ := filepath.Glob(filepath.Join(dir, "serial-data-*.sqlite3"))
	if len(files) < 2 {
		t.Fatalf("expected rotation, files = %v", files)
	}
	rows := 0
	for _, f := range files {
		db, err := sql.Open("sqlite", f)
		if err != nil {
			t.Fatal(err)
		}
		r, err := db.Query(`SELECT COALESCE(s.mode, ''), s.ended_at IS NOT NULL, r.size_bytes FROM received_data r LEFT JOIN sessions s ON s.id = r.session_id WHERE r.source = 'vs'`)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var mode string
			var ended bool
			var size int
			if err := r.Scan(&mode, &ended, &size); err != nil {
				t.Fatal(err)
			}
			rows++
			if mode != "虚拟串口" {
				t.Errorf("%s: %d 字节的虚拟串口数据挂在会话 %q 上", filepath.Base(f), size, mode)
			}
			if !ended {
				t.Errorf("%s: 虚拟串口会话未结束", filepath.Base(f))
			}
		}
		r.Close()
		db.Close()
	}
	if rows != 2 {
		t.Fatalf("virtual serial rows = %d, want 2", rows)
	}
}

func TestStoreSettings(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.GetSetting("deepseek.api_key"); got != "" {
		t.Fatalf("default setting = %q", got)
	}
	if err := store.SetSetting("deepseek.api_key", "test-key"); err != nil {
		t.Fatal(err)
	}
	if got := store.GetSetting("deepseek.api_key"); got != "test-key" {
		t.Fatalf("setting = %q", got)
	}
}

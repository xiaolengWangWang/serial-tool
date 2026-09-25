package wincore

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteStoresRawTextAndRotates(t *testing.T) {
	storage.Lock()
	storage.dir = t.TempDir()
	if err := openDatabaseFileLocked(time.Now().Add(-24*time.Hour), false); err != nil {
		storage.Unlock()
		t.Fatal(err)
	}
	storage.mode, storage.endpoint, storage.parameters = "TCP 服务端", ":9000", "test"
	if err := insertSessionLocked(time.Now()); err != nil {
		storage.Unlock()
		t.Fatal(err)
	}
	oldPath := storage.path
	storage.Unlock()

	if err := storeReceived("TCP 127.0.0.1:1234", []byte("你好")); err != nil {
		t.Fatal(err)
	}
	storage.Lock()
	newPath, db := storage.path, storage.db
	storage.Unlock()
	if oldPath == newPath || !strings.Contains(filepath.Base(newPath), time.Now().Format("20060102")) {
		t.Fatalf("未按日期滚动: %s -> %s", oldPath, newPath)
	}
	var text string
	var valid bool
	if err := db.QueryRow(`SELECT text_data, is_utf8 FROM received_data ORDER BY id DESC LIMIT 1`).Scan(&text, &valid); err != nil {
		t.Fatal(err)
	}
	if text != "你好" || !valid {
		t.Fatalf("字符串保存失败: %q, utf8=%v", text, valid)
	}

	databaseSizeLimit = 1
	raw := []byte{0x00, 0xff, 0x41}
	if err := storeReceived("串口", raw); err != nil {
		t.Fatal(err)
	}
	storage.Lock()
	rotatedPath, db := storage.path, storage.db
	storage.Unlock()
	if rotatedPath == newPath {
		t.Fatal("未按大小滚动数据库")
	}
	var saved []byte
	if err := db.QueryRow(`SELECT raw_data, is_utf8 FROM received_data ORDER BY id DESC LIMIT 1`).Scan(&saved, &valid); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, raw) || valid {
		t.Fatalf("原始数据保存失败: % X, utf8=%v", saved, valid)
	}

	storage.Lock()
	finishSessionLocked(time.Now())
	_ = storage.db.Close()
	storage.db, storage.sessionID = nil, 0
	storage.dir, storage.path, storage.date = "", "", ""
	storage.index, storage.initErr, storage.errorReported = 0, nil, false
	storage.Unlock()
	databaseSizeLimit = defaultDatabaseSizeLimit
}

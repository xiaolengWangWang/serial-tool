package wincore

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const analysisStart = "2026-09-10T00:00:00Z"
const analysisEnd = "2026-09-10T00:00:02Z"

func analysisTestDB(t *testing.T) (string, string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(store.path)
	store.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, mode := range []string{"TCP 客户端", "UDP 服务端", "串口"} {
		if _, err := db.Exec(`INSERT INTO sessions(started_at, mode, endpoint, parameters) VALUES (?, ?, '', '')`, analysisStart, mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir, name, db
}

func insertAnalysisPacket(t *testing.T, db *sql.DB, session int, at, source string, raw []byte) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO received_data(session_id, received_at, source, size_bytes, raw_data, text_data) VALUES (?, ?, ?, ?, ?, 'NOT THE RAW PACKET')`, session, at, source, len(raw), raw)
	if err != nil {
		t.Fatal(err)
	}
}

func requireAnalysisContains(t *testing.T, report string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(report, want) {
			t.Fatalf("missing %q in report:\n%s", want, report)
		}
	}
}

func TestDatabaseAnalysisFilteringAndRawProtocol(t *testing.T) {
	dir, name, db := analysisTestDB(t)
	// Insert out of chronological order, including equivalent offset timestamps.
	insertAnalysisPacket(t, db, 2, "2026-09-09T19:00:02-05:00", "peer:9000", []byte{0xff, 0})
	insertAnalysisPacket(t, db, 1, "2026-09-10T08:00:00+08:00", "发送", []byte{0, 1, 0, 0, 0, 6, 1, 3, 0, 0, 0, 10})
	insertAnalysisPacket(t, db, 3, "2026-09-10T00:00:01Z", "接收", []byte{1, 3, 0, 0, 0, 10, 0xc5, 0xcd})
	insertAnalysisPacket(t, db, 1, "2026-09-10T00:00:01Z", "断开", []byte("connection lost"))
	insertAnalysisPacket(t, db, 1, "2026-09-10T00:00:02.001Z", "发送", []byte{9})
	insertAnalysisPacket(t, db, 1, "2026-09-09T23:59:59.999Z", "接收", []byte{9})
	for _, tc := range []struct{ direction, counts string }{
		{"ALL", "筛选总计：3 条；RX：2 条 / 10 字节；TX：1 条 / 12 字节"},
		{"RX", "筛选总计：2 条；RX：2 条 / 10 字节；TX：0 条 / 0 字节"},
		{"TX", "筛选总计：1 条；RX：0 条 / 0 字节；TX：1 条 / 12 字节"},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			report, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, tc.direction, 100)
			if err != nil {
				t.Fatal(err)
			}
			requireAnalysisContains(t, report, tc.counts)
			if tc.direction == "ALL" {
				requireAnalysisContains(t, report, "Modbus TCP：事务标识 0x0001", "Modbus CRC：通过", "UDP 报文分析", "首字节：0xFF", "实际详细解析：3 条")
				if strings.Index(report, "记录 #2 |") > strings.Index(report, "记录 #3 |") || strings.Index(report, "记录 #3 |") > strings.Index(report, "记录 #1 |") {
					t.Fatal("not chronological")
				}
			}
		})
	}
	report, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 2)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "筛选总计：3 条", "实际选中：2 条；未选中：1 条", "因记录上限省略：1 条", "记录 #3 |", "记录 #1 |")
	if strings.Contains(report, "记录 #2 |") {
		t.Fatal("selected oldest instead of newest")
	}
	report, err = AnalyzeDatabase(dir, name, "2026-09-10T08:00:00+08:00", analysisStart, "ALL", 100)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "筛选总计：1 条")
}

func TestDatabaseAnalysisEmptyAndInvalid(t *testing.T) {
	dir, name, db := analysisTestDB(t)
	report, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 2000)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "筛选总计：0 条", "实际详细解析：0 条", "没有符合条件")
	for _, tc := range []struct {
		start, end, direction string
		limit                 int
	}{
		{"", analysisEnd, "ALL", 100},
		{"2026-09-10 00:00:00", analysisEnd, "ALL", 100},
		{analysisStart, "invalid", "ALL", 100},
		{analysisEnd, analysisStart, "ALL", 100},
		{analysisStart, analysisEnd, "rx", 100},
		{analysisStart, analysisEnd, "ALL", 0},
		{analysisStart, analysisEnd, "ALL", -1},
		{analysisStart, analysisEnd, "ALL", 1000001},
	} {
		if _, err := AnalyzeDatabase(dir, name, tc.start, tc.end, tc.direction, tc.limit); err == nil {
			t.Fatalf("accepted invalid options: %+v", tc)
		}
	}
	insertAnalysisPacket(t, db, 3, analysisStart, "接收", []byte{})
	report, err = AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 100)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "实际选中：1 条", "0 字节")
	if strings.Contains(report, "分析失败") {
		t.Fatal("empty stored payload is not a HEX input error")
	}
}

func TestDatabaseAnalysisSelectionCaps(t *testing.T) {
	dir, name, db := analysisTestDB(t)
	for i := 0; i < 23; i++ {
		insertAnalysisPacket(t, db, 2, analysisStart, "发送", []byte{0xff})
	}
	report, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 100)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "实际选中：23 条", "实际详细解析：20 条", "选中但未详细解析：3 条")
	if strings.Count(report, "UDP 报文分析") != 20 || strings.Count(report, "记录 #") != 20 {
		t.Fatal(report)
	}
	// Length is checked in SQLite before fetching any oversized raw BLOB.
	if _, err := db.Exec(`INSERT INTO received_data(session_id, received_at, source, size_bytes, raw_data) VALUES (1, ?, '发送', ?, zeroblob(?))`, analysisEnd, analysisByteLimit+1, analysisByteLimit+1); err != nil {
		t.Fatal(err)
	}
	report, err = AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 100)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "筛选总计：24 条", "TX：24 条 / 8388632 字节", "实际选中：0 条", "字节上限触发：true", "因字节上限省略：24 条", "实际详细解析：0 条")
	if _, err := db.Exec(`UPDATE received_data SET raw_data = zeroblob(?), size_bytes = ? WHERE id = 24`, analysisByteLimit, analysisByteLimit); err != nil {
		t.Fatal(err)
	}
	report, err = AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 100)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "实际选中：1 条", "选中负载：8388608 字节", "因字节上限省略：23 条", "长度：8388608 字节")
}

func TestDatabaseAnalysisMillionRecordLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("百万条压力测试使用非 short 模式单独验证")
	}
	dir, name, db := analysisTestDB(t)
	// One more than the requested ceiling verifies newest selection, not just
	// acceptance of the option. Small packets stay below the payload cap.
	_, err := db.Exec(`WITH RECURSIVE seq(n) AS (
        VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n < 1000001
    ) INSERT INTO received_data(session_id, received_at, source, size_bytes, raw_data)
      SELECT 2, ?, '发送', 1, x'FF' FROM seq`, analysisStart)
	if err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 1000000)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "筛选总计：1000001 条", "实际选中：1000000 条；未选中：1 条",
		"选中负载：1000000 字节", "实际详细解析：20 条", "选中但未详细解析：999980 条",
		"记录 #2 |", "记录 #21 |")
	if strings.Contains(report, "记录 #1 |") || strings.Contains(report, "记录 #22 |") || strings.Count(report, "记录 #") != 20 || len(report) > 32768 {
		t.Fatal("large query did not keep the report bounded to the oldest 20 selected records")
	}
}

func TestDatabaseAnalysisFilesAndReadOnly(t *testing.T) {
	dir, name, writer := analysisTestDB(t)
	insertAnalysisPacket(t, writer, 1, analysisStart, "发送", []byte{1})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "serial-data-directory.sqlite3"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(dir, "serial-data-link.sqlite3")); err != nil {
		t.Fatal(err)
	}
	files, err := ListAnalysisDatabases(dir)
	if err != nil || len(files) != 1 || files[0] != name {
		t.Fatalf("files %v: %v", files, err)
	}
	for _, bad := range []string{"", "../" + name, path, "sub/" + name, "sub\\" + name, "commbox-settings.sqlite3", "serial-data-link.sqlite3", "serial-data-directory.sqlite3", "serial-data-missing.sqlite3", name + "?mode=rw", "serial-data-\x00.sqlite3"} {
		if _, err := AnalyzeDatabase(dir, bad, analysisStart, analysisEnd, "ALL", 100); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "serial-data-missing.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("created missing database")
	}
	db, err := openAnalysisDatabase(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE forbidden_write (id INTEGER)`); err == nil {
		t.Fatal("read-only connection allowed write")
	}
	if _, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 100); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("database changed: %v", err)
	}
	empty, err := ListAnalysisDatabases(t.TempDir())
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty files: %v %v", empty, err)
	}
	if _, err := ListAnalysisDatabases(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestDatabaseAnalysisSchemaAndCorruptionErrors(t *testing.T) {
	for _, kind := range []string{"missing tables", "missing raw_data", "corrupt", "null raw_data"} {
		t.Run(kind, func(t *testing.T) {
			dir, name, db := analysisTestDB(t)
			switch kind {
			case "missing tables":
				if _, err := db.Exec(`DROP TABLE sessions`); err != nil {
					t.Fatal(err)
				}
			case "missing raw_data":
				if _, err := db.Exec(`ALTER TABLE received_data RENAME COLUMN raw_data TO missing`); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				db.Close()
				if err := os.WriteFile(filepath.Join(dir, name), []byte("not a SQLite database"), 0600); err != nil {
					t.Fatal(err)
				}
			case "null raw_data":
				if _, err := db.Exec(`DROP TABLE received_data; CREATE TABLE received_data (id INTEGER, session_id INTEGER, received_at TEXT, source TEXT, size_bytes INTEGER, raw_data BLOB); INSERT INTO received_data VALUES (1, 1, '2026-09-10T00:00:00Z', '发送', 0, NULL)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, "ALL", 100); err == nil {
				t.Fatal("invalid schema/data accepted")
			}
		})
	}
}

func TestDatabaseAnalysisTransportNormalization(t *testing.T) {
	for mode, want := range map[string]string{"TCP 服务端": "TCP", " tcp client ": "TCP", "UDP 客户端": "UDP", "串口服务器": "串口", "serial": "串口", "HTTP 客户端": "HTTP 客户端"} {
		if got := analysisTransport(mode); got != want {
			t.Fatalf("%q: got %q, want %q", mode, got, want)
		}
	}
}

func TestDatabaseAnalysisVirtualSerialDirections(t *testing.T) {
	dir, name, db := analysisTestDB(t)
	insertAnalysisPacket(t, db, 3, analysisStart, "虚拟串口 #12 发送", []byte{1, 2})
	insertAnalysisPacket(t, db, 3, analysisStart, "虚拟串口 #12 接收", []byte{3})
	insertAnalysisPacket(t, db, 3, analysisStart, "发送", []byte{4})
	for _, tc := range []struct{ direction, counts string }{
		{"ALL", "筛选总计：3 条；RX：1 条 / 1 字节；TX：2 条 / 3 字节"},
		{"TX", "筛选总计：2 条；RX：0 条 / 0 字节；TX：2 条 / 3 字节"},
		{"RX", "筛选总计：1 条；RX：1 条 / 1 字节；TX：0 条 / 0 字节"},
	} {
		report, err := AnalyzeDatabase(dir, name, analysisStart, analysisEnd, tc.direction, 100)
		if err != nil {
			t.Fatal(err)
		}
		requireAnalysisContains(t, report, tc.counts)
		if tc.direction != "RX" {
			requireAnalysisContains(t, report, "记录 #1 | "+analysisStart+" | TX |")
		}
		if tc.direction != "TX" {
			requireAnalysisContains(t, report, "记录 #2 | "+analysisStart+" | RX |")
		}
	}
}

func TestDatabaseAnalysisMultipleFiles(t *testing.T) {
	dir, first, a := analysisTestDB(t)
	otherDir, otherName, b := analysisTestDB(t)
	insertAnalysisPacket(t, a, 2, analysisStart, "发送", []byte{0xff})
	insertAnalysisPacket(t, a, 2, analysisEnd, "接收", []byte{0xfe})
	insertAnalysisPacket(t, b, 2, "2026-09-10T08:00:01+08:00", "发送", []byte{0xfd})
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	second := "serial-data-second.sqlite3"
	if err := os.Rename(filepath.Join(otherDir, otherName), filepath.Join(dir, second)); err != nil {
		t.Fatal(err)
	}
	report, err := AnalyzeDatabases(dir, []string{first, second, first}, analysisStart, analysisEnd, "ALL", 2)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "数据库文件：2 个", "筛选总计：3 条；RX：1 条 / 1 字节；TX：2 条 / 2 字节", "实际选中：2 条；未选中：1 条",
		"来源文件："+second+"\n记录 #1 |", "来源文件："+first+"\n记录 #2 |")
	if strings.Contains(report, "首字节：0xFF") || strings.Index(report, "来源文件："+second) > strings.Index(report, "来源文件："+first) {
		t.Fatal("not globally newest two in chronological order")
	}
	report, err = AnalyzeDatabases(dir, []string{first, second}, analysisStart, analysisEnd, "TX", 1)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "筛选总计：2 条；RX：0 条 / 0 字节；TX：2 条 / 2 字节", "实际选中：1 条；未选中：1 条", "首字节：0xFD")
	for _, names := range [][]string{nil, {}, {first, "serial-data-missing.sqlite3"}, {first, "../" + first}} {
		if _, err := AnalyzeDatabases(dir, names, analysisStart, analysisEnd, "ALL", 1000000); err == nil {
			t.Fatalf("invalid selection accepted: %v", names)
		}
	}
	// The shared byte ceiling must not restart at a file boundary.
	if _, err := a.Exec(`UPDATE received_data SET raw_data=zeroblob(?), size_bytes=? WHERE id=2`, analysisByteLimit, analysisByteLimit); err != nil {
		t.Fatal(err)
	}
	report, err = AnalyzeDatabases(dir, []string{first, second}, analysisStart, analysisEnd, "ALL", 1000000)
	if err != nil {
		t.Fatal(err)
	}
	requireAnalysisContains(t, report, "实际选中：1 条；未选中：2 条", "选中负载：8388608 字节", "因字节上限省略：2 条")
}

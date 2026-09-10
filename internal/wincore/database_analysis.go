package wincore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const analysisByteLimit int64 = 8 << 20
const analysisDetailLimit = 20

func analysisFilename(name string) bool {
	return filepath.Base(name) == name && !strings.ContainsAny(name, "/\\\x00") &&
		strings.HasPrefix(name, "serial-data-") && strings.HasSuffix(name, ".sqlite3")
}

// ListAnalysisDatabases lists regular capture files only, in filename order.
func ListAnalysisDatabases(dir string) ([]string, error) {
	files := make([]string, 0)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files, fmt.Errorf("读取数据库目录失败：%w", err)
	}
	for _, entry := range entries {
		if !analysisFilename(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return files, fmt.Errorf("读取数据库文件信息失败：%w", err)
		}
		if info.Mode().IsRegular() {
			files = append(files, entry.Name())
		}
	}
	return files, nil
}

func openAnalysisDatabase(dir, filename string) (*sql.DB, error) {
	if !analysisFilename(filename) {
		return nil, fmt.Errorf("仅允许数据目录内的 serial-data-*.sqlite3 基础文件名")
	}
	path, err := filepath.Abs(filepath.Join(dir, filename))
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("数据库文件不可用：%w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("数据库必须是普通文件，不接受符号链接或目录")
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func analysisTransport(mode string) string {
	mode = strings.ToUpper(strings.TrimSpace(mode))
	switch {
	case strings.HasPrefix(mode, "TCP"):
		return "TCP"
	case strings.HasPrefix(mode, "UDP"):
		return "UDP"
	case strings.Contains(mode, "串口"), strings.HasPrefix(mode, "SERIAL"):
		return "串口"
	default:
		return mode
	}
}

// AnalyzeDatabase uses one read snapshot. The caller should invoke it off the UI
// thread. Bounds are inclusive RFC3339 instants; limit must be in [1, 2000].
// No capture store is opened, migrated, or modified by this operation.
func AnalyzeDatabase(dir, filename, start, end, direction string, limit int) (string, error) {
	from, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return "", fmt.Errorf("开始时间必须为 RFC3339：%w", err)
	}
	to, err := time.Parse(time.RFC3339Nano, end)
	if err != nil {
		return "", fmt.Errorf("结束时间必须为 RFC3339：%w", err)
	}
	if from.After(to) {
		return "", fmt.Errorf("开始时间不能晚于结束时间")
	}
	if direction != "ALL" && direction != "RX" && direction != "TX" {
		return "", fmt.Errorf("方向必须为 ALL、RX 或 TX")
	}
	if limit < 1 || limit > 2000 {
		return "", fmt.Errorf("记录上限必须在 1 至 2000 之间")
	}
	db, err := openAnalysisDatabase(dir, filename)
	if err != nil {
		return "", err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", fmt.Errorf("打开只读数据库失败：%w", err)
	}
	defer tx.Rollback()
	const filter = ` FROM (SELECT *, CASE WHEN source = '发送' OR source GLOB '虚拟串口 #[0-9]* 发送'
        THEN 'TX' ELSE 'RX' END AS direction FROM received_data) r JOIN sessions s ON s.id = r.session_id
		WHERE r.source <> '断开'
		AND julianday(r.received_at) BETWEEN julianday(?) AND julianday(?)
		AND (? = 'ALL' OR r.direction = ?)`
	args := []any{from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano), direction, direction}
	var total, rxCount, txCount, rxBytes, txBytes int64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN r.direction = 'RX' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.direction = 'TX' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.direction = 'RX' THEN r.size_bytes ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.direction = 'TX' THEN r.size_bytes ELSE 0 END), 0)`+filter, args...).Scan(
		&total, &rxCount, &txCount, &rxBytes, &txBytes)
	if err != nil {
		return "", fmt.Errorf("统计数据库失败（请检查存储 schema）：%w", err)
	}
	// Fetch lengths before BLOBs, so even a single oversized row cannot defeat
	// the payload budget. Ties use id to keep the latest selection deterministic.
	rows, err := tx.QueryContext(ctx, `SELECT r.id, r.received_at, r.direction, s.mode, length(r.raw_data)`+filter+
		` ORDER BY julianday(r.received_at) DESC, r.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return "", fmt.Errorf("读取记录失败：%w", err)
	}
	type packet struct {
		id, size            int64
		at, direction, mode string
	}
	var selected []packet
	var rawBytes int64
	candidates := 0
	byteCapped := false
	for rows.Next() {
		var p packet
		if err := rows.Scan(&p.id, &p.at, &p.direction, &p.mode, &p.size); err != nil {
			rows.Close()
			return "", fmt.Errorf("读取记录字段失败：%w", err)
		}
		candidates++
		if p.size < 0 {
			rows.Close()
			return "", fmt.Errorf("记录 %d 的原始负载长度无效", p.id)
		}
		if byteCapped || p.size > analysisByteLimit-rawBytes {
			byteCapped = true
			continue
		}
		rawBytes += p.size
		selected = append(selected, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("读取记录失败：%w", err)
	}
	if err := rows.Close(); err != nil {
		return "", fmt.Errorf("关闭记录查询失败：%w", err)
	}
	details := min(len(selected), analysisDetailLimit)
	var out strings.Builder
	fmt.Fprintf(&out, "本地 SQLite 数据分析\n数据库：%s\n时间范围（含边界）：%s 至 %s\n方向：%s\n", filename, start, end, direction)
	fmt.Fprintf(&out, "筛选总计：%d 条；RX：%d 条 / %d 字节；TX：%d 条 / %d 字节\n", total, rxCount, rxBytes, txCount, txBytes)
	out.WriteString("总量字节依据 size_bytes；已排除 source=断开；普通发送与虚拟串口发送为 TX，其余为 RX。\n")
	fmt.Fprintf(&out, "最新记录上限：%d 条；候选：%d 条；实际选中：%d 条；未选中：%d 条\n", limit, candidates, len(selected), total-int64(len(selected)))
	fmt.Fprintf(&out, "原始负载上限：8 MiB（%d 字节）；选中负载：%d 字节；字节上限触发：%t\n", analysisByteLimit, rawBytes, byteCapped)
	fmt.Fprintf(&out, "因记录上限省略：%d 条；因字节上限省略：%d 条（从首条超限记录起停止选取，不截断报文）。\n", total-int64(candidates), candidates-len(selected))
	fmt.Fprintf(&out, "详细解析上限：%d 条；实际详细解析：%d 条；选中但未详细解析：%d 条\n", analysisDetailLimit, details, len(selected)-details)
	out.WriteString("选中记录按时间正序展示，详细解析正序前 20 条的原始 raw_data；不使用界面 HEX 或 text_data。\n单条存储记录可能是半包或多包；协议识别及 CRC 提示来自单条解析器，仅作候选，不推断请求响应匹配或通信故障。\n")
	if total == 0 {
		out.WriteString("没有符合条件的报文。\n")
	}
	for i := len(selected) - 1; i >= 0; i-- {
		p := selected[i]
		fmt.Fprintf(&out, "\n记录 #%d | %s | %s | %s | %d 字节\n", p.id, p.at, p.direction, analysisTransport(p.mode), p.size)
		if len(selected)-1-i >= details {
			continue
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT raw_data FROM received_data WHERE id = ?`, p.id).Scan(&raw); err != nil {
			return "", fmt.Errorf("读取记录 %d 原始负载失败：%w", p.id, err)
		}
		if int64(len(raw)) != p.size {
			return "", fmt.Errorf("记录 %d 原始负载长度不一致", p.id)
		}
		out.WriteString(AnalyzeTransportPacket(analysisTransport(p.mode), hex.EncodeToString(raw)))
		out.WriteByte('\n')
	}
	return out.String(), nil
}

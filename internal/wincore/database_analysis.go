package wincore

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const analysisByteLimit int64 = 8 << 20
const analysisDetailLimit = 20
const analysisRecordLimit = 1_000_000

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
	// 采集库处于 WAL 模式，数据可能仍在 -wal 文件中；mode=ro 只读连接看不到 WAL，
	// 会把正在写入的库误判为空库或缺表。改为正常打开以读取已提交的 WAL 内容，
	// 并用 query_only 保证本分析连接只读不写，busy_timeout 容忍并发写入方短暂持锁。
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "query_only(true)")
	uri := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
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

type analysisSource struct {
	name                                      string
	tx                                        *sql.Tx
	rows                                      *sql.Rows
	total, rxCount, txCount, rxBytes, txBytes int64
}

type analysisPacket struct {
	source              *analysisSource
	id, size            int64
	at, direction, mode string
	order               float64
}

// Only one head per file is kept while merging, never a million Go records.
type analysisQueue []analysisPacket

func (q analysisQueue) Len() int { return len(q) }
func (q analysisQueue) Less(i, j int) bool {
	if q[i].order != q[j].order {
		return q[i].order > q[j].order
	}
	if q[i].source.name != q[j].source.name {
		return q[i].source.name > q[j].source.name
	}
	return q[i].id > q[j].id
}
func (q analysisQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *analysisQueue) Push(x any)   { *q = append(*q, x.(analysisPacket)) }
func (q *analysisQueue) Pop() any     { old := *q; p := old[len(old)-1]; *q = old[:len(old)-1]; return p }

func (s *analysisSource) next() (analysisPacket, bool, error) {
	p := analysisPacket{source: s}
	if !s.rows.Next() {
		return p, false, s.rows.Err()
	}
	if err := s.rows.Scan(&p.id, &p.at, &p.direction, &p.mode, &p.size, &p.order); err != nil {
		return p, false, err
	}
	if p.size < 0 {
		return p, false, fmt.Errorf("记录 %d 的原始负载长度无效", p.id)
	}
	return p, true, nil
}

func AnalyzeDatabase(dir, filename, start, end, direction string, limit int) (string, error) {
	return AnalyzeDatabases(dir, []string{filename}, start, end, direction, limit)
}

// AnalyzeDatabases merges independently acquired read-only file snapshots.
// Bounds are inclusive RFC3339 instants; the newest-record limit is global.
// Run off the UI thread. No store is opened, migrated, or modified.
func AnalyzeDatabases(dir string, filenames []string, start, end, direction string, limit int) (string, error) {
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
	if limit < 1 || limit > analysisRecordLimit {
		return "", fmt.Errorf("记录上限必须在 1 至 %d 之间", analysisRecordLimit)
	}
	if len(filenames) == 0 {
		return "", fmt.Errorf("请至少选择一个数据库文件")
	}
	seen := make(map[string]bool)
	var names []string
	for _, name := range filenames {
		if !analysisFilename(name) {
			return "", fmt.Errorf("数据库文件名无效：%q", name)
		}
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	sort.Strings(names)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const filter = ` FROM (SELECT *, CASE WHEN source = '发送' OR source GLOB '虚拟串口 #[0-9]* 发送'
        THEN 'TX' ELSE 'RX' END AS direction FROM received_data) r JOIN sessions s ON s.id = r.session_id
		WHERE r.source <> '断开'
		AND julianday(r.received_at) BETWEEN julianday(?) AND julianday(?)
		AND (? = 'ALL' OR r.direction = ?)`
	args := []any{from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano), direction, direction}
	var total, rxCount, txCount, rxBytes, txBytes int64
	var sources []*analysisSource
	queue := &analysisQueue{}
	for _, name := range names {
		db, err := openAnalysisDatabase(dir, name)
		if err != nil {
			return "", fmt.Errorf("数据库 %s：%w", name, err)
		}
		defer db.Close()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return "", fmt.Errorf("打开只读数据库 %s 失败：%w", name, err)
		}
		defer tx.Rollback()
		s := &analysisSource{name: name, tx: tx}
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN r.direction = 'RX' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.direction = 'TX' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.direction = 'RX' THEN r.size_bytes ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.direction = 'TX' THEN r.size_bytes ELSE 0 END), 0)`+filter, args...).Scan(
			&s.total, &s.rxCount, &s.txCount, &s.rxBytes, &s.txBytes)
		if err != nil {
			return "", fmt.Errorf("统计数据库 %s 失败（请检查存储 schema）：%w", name, err)
		}
		total += s.total
		rxCount += s.rxCount
		txCount += s.txCount
		rxBytes += s.rxBytes
		txBytes += s.txBytes
		// Fetch lengths before BLOBs; each file contributes at most the global limit.
		s.rows, err = tx.QueryContext(ctx, `SELECT r.id, r.received_at, r.direction, s.mode, length(r.raw_data), julianday(r.received_at)`+filter+
			` ORDER BY julianday(r.received_at) DESC, r.id DESC LIMIT ?`, append(args, limit)...)
		if err != nil {
			return "", fmt.Errorf("读取数据库 %s 失败：%w", name, err)
		}
		defer s.rows.Close()
		sources = append(sources, s)
		p, ok, err := s.next()
		if err != nil {
			return "", fmt.Errorf("读取数据库 %s 失败：%w", name, err)
		}
		if ok {
			heap.Push(queue, p)
		}
	}
	var selected [analysisDetailLimit]analysisPacket
	selectedCount := 0
	var rawBytes int64
	candidates := min(total, int64(limit))
	byteCapped := false
	for int64(selectedCount) < candidates && queue.Len() > 0 {
		p := heap.Pop(queue).(analysisPacket)
		if p.size > analysisByteLimit-rawBytes {
			byteCapped = true
			break
		}
		rawBytes += p.size
		selected[selectedCount%analysisDetailLimit] = p
		selectedCount++
		if int64(selectedCount) == candidates {
			break
		}
		next, ok, err := p.source.next()
		if err != nil {
			return "", fmt.Errorf("读取数据库 %s 失败：%w", p.source.name, err)
		}
		if ok {
			heap.Push(queue, next)
		}
	}
	for _, s := range sources {
		if err := s.rows.Close(); err != nil {
			return "", fmt.Errorf("关闭数据库 %s 查询失败：%w", s.name, err)
		}
	}
	if !byteCapped && int64(selectedCount) != candidates {
		return "", fmt.Errorf("数据库记录数量不一致，分析已取消")
	}
	details := min(selectedCount, analysisDetailLimit)
	var out strings.Builder
	fmt.Fprintf(&out, "本地 SQLite 数据分析\n数据库文件：%d 个\n时间范围（含边界）：%s 至 %s\n方向：%s\n", len(sources), start, end, direction)
	for _, s := range sources {
		fmt.Fprintf(&out, "文件：%s；筛选 %d 条；RX %d 条 / %d 字节；TX %d 条 / %d 字节\n", s.name, s.total, s.rxCount, s.rxBytes, s.txCount, s.txBytes)
	}
	out.WriteString("各文件独立只读快照，不保证采集中多个文件处于同一瞬间；同名选择已去重，不同文件中的重复采集记录不去重。\n")
	fmt.Fprintf(&out, "筛选总计：%d 条；RX：%d 条 / %d 字节；TX：%d 条 / %d 字节\n", total, rxCount, rxBytes, txCount, txBytes)
	out.WriteString("总量字节依据 size_bytes；已排除 source=断开；普通发送与虚拟串口发送为 TX，其余为 RX。\n")
	fmt.Fprintf(&out, "最新记录上限：%d 条（所有文件合计）；候选：%d 条；实际选中：%d 条；未选中：%d 条\n", limit, candidates, selectedCount, total-int64(selectedCount))
	fmt.Fprintf(&out, "原始负载上限：8 MiB（%d 字节）；选中负载：%d 字节；字节上限触发：%t\n", analysisByteLimit, rawBytes, byteCapped)
	fmt.Fprintf(&out, "因记录上限省略：%d 条；因字节上限省略：%d 条（从首条超限记录起停止选取，不截断报文）。\n", total-candidates, candidates-int64(selectedCount))
	fmt.Fprintf(&out, "详细解析上限：%d 条；实际详细解析：%d 条；选中但未详细解析：%d 条\n", analysisDetailLimit, details, selectedCount-details)
	out.WriteString("仅展开选中范围时间正序前 20 条详情，其余只计入统计，不生成逐条列表。相同时间按文件名、记录 ID 排序。解析完整 raw_data，不使用界面 HEX 或 text_data。\n单条存储记录可能是半包或多包；协议识别及 CRC 提示来自单条解析器，仅作候选，不推断请求响应匹配或通信故障。\n")
	if total == 0 {
		out.WriteString("没有符合条件的报文。\n")
	}
	for i := selectedCount - 1; i >= selectedCount-details; i-- {
		p := selected[i%analysisDetailLimit]
		fmt.Fprintf(&out, "\n来源文件：%s\n记录 #%d | %s | %s | %s | %d 字节\n", p.source.name, p.id, p.at, p.direction, analysisTransport(p.mode), p.size)
		var raw []byte
		if err := p.source.tx.QueryRowContext(ctx, `SELECT raw_data FROM received_data WHERE id = ?`, p.id).Scan(&raw); err != nil {
			return "", fmt.Errorf("读取数据库 %s 记录 %d 原始负载失败：%w", p.source.name, p.id, err)
		}
		if int64(len(raw)) != p.size {
			return "", fmt.Errorf("记录 %d 原始负载长度不一致", p.id)
		}
		out.WriteString(AnalyzeTransportPacket(analysisTransport(p.mode), hex.EncodeToString(raw)))
		out.WriteByte('\n')
	}
	return out.String(), nil
}

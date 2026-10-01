package aiattachment

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	_ "golang.org/x/image/webp"
)

const (
	MaxTextBytes   = 64 << 10
	MaxImageBytes  = 2 << 20
	MaxImagePixels = 32_000_000
	maxDocument    = 5 << 20
)

type Attachment struct {
	Name    string
	Text    string
	DataURL string
}

type Turn struct {
	Role    string       `json:"role"`
	Content string       `json:"content"`
	Files   []Attachment `json:"-"`
}

func (t Turn) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}{t.Role, Content(t.Content, t.Files)})
}

func ValidateTurns(turns []Turn) error {
	if len(turns) == 0 {
		return errors.New("对话为空")
	}
	textBytes, imageBytes := 0, 0
	for _, turn := range turns {
		if turn.Role != "user" && turn.Role != "assistant" {
			return errors.New("对话角色无效")
		}
		if turn.Role != "user" && len(turn.Files) > 0 {
			return errors.New("只有提问可以附带文件")
		}
		textBytes += len(turn.Content)
		for _, file := range turn.Files {
			textBytes += len(file.Name) + len(file.Text) + 32
			imageBytes += len(file.DataURL)
		}
	}
	if textBytes > 512<<10 {
		return errors.New("对话文字超过 512 KiB，请清空对话或减少附件")
	}
	if imageBytes > 24<<20 {
		return errors.New("对话图片超过 24 MiB，请清空对话或减少附件")
	}
	return nil
}

// Read extracts text locally or encodes an image for a vision-capable chat endpoint.
func Read(path string) (Attachment, error) {
	name := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		return Attachment{}, err
	}
	if !info.Mode().IsRegular() {
		return Attachment{}, fmt.Errorf("%s 不是普通文件", name)
	}
	ext := strings.ToLower(filepath.Ext(name))
	limit := int64(MaxTextBytes)
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		limit = MaxImageBytes
	case ".pdf", ".docx":
		limit = maxDocument
	case ".txt", ".log", ".csv", ".json", ".md", ".xml", ".yaml", ".yml":
	default:
		return Attachment{}, fmt.Errorf("%s：仅支持文本、PDF、DOCX 和常见图片", name)
	}
	if info.Size() == 0 {
		return Attachment{}, fmt.Errorf("%s 是空文件", name)
	}
	if info.Size() > limit {
		return Attachment{}, fmt.Errorf("%s 超过 %d KiB 的附件大小限制", name, limit>>10)
	}
	var a Attachment
	a.Name = name
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		data, err := os.ReadFile(path)
		if err != nil {
			return Attachment{}, err
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			return Attachment{}, fmt.Errorf("%s 不是有效图片", name)
		}
		if int64(cfg.Width)*int64(cfg.Height) > MaxImagePixels {
			return Attachment{}, fmt.Errorf("%s 超过 3200 万像素，请缩小图片分辨率", name)
		}
		if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
			return Attachment{}, fmt.Errorf("%s 不是有效图片", name)
		}
		mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}[format]
		if mime == "" {
			return Attachment{}, fmt.Errorf("%s 图片格式不支持", name)
		}
		a.DataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	case ".docx":
		a.Text, err = docxText(path)
	case ".pdf":
		a.Text, err = pdfText(path)
	default:
		var data []byte
		data, err = os.ReadFile(path)
		if err == nil {
			switch {
			case len(data) >= 2 && ((data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)):
				if len(data)%2 != 0 {
					err = errors.New("UTF-16 文本长度无效")
					break
				}
				var order binary.ByteOrder = binary.LittleEndian
				if data[0] == 0xfe {
					order = binary.BigEndian
				}
				codes := make([]uint16, (len(data)-2)/2)
				for i := range codes {
					codes[i] = order.Uint16(data[2+i*2:])
				}
				a.Text = string(utf16.Decode(codes))
			case !utf8.Valid(data) || strings.ContainsRune(string(data), 0):
				err = errors.New("不是 UTF-8 或 UTF-16 文本文件")
			default:
				a.Text = strings.TrimPrefix(string(data), "\ufeff")
			}
		}
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("%s：%w", name, err)
	}
	if a.DataURL == "" && strings.TrimSpace(a.Text) == "" {
		if ext == ".pdf" {
			return Attachment{}, fmt.Errorf("%s 没有可提取的文字；扫描版 PDF 暂不支持", name)
		}
		return Attachment{}, fmt.Errorf("%s 没有可提取的文字", name)
	}
	return a, nil
}

func pdfText(path string) (string, error) {
	f, reader, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	pages := reader.NumPage()
	if pages < 1 || pages > 500 {
		return "", errors.New("PDF 页数无效或超过 500 页")
	}
	var b strings.Builder
	for page := 1; page <= pages; page++ {
		if err := pdfPageBudget(reader.Page(page).V.Key("Contents")); err != nil {
			return "", err
		}
		text, err := reader.Page(page).GetPlainText(nil)
		if err != nil {
			return "", err
		}
		if b.Len()+len(text) > MaxTextBytes {
			return "", errors.New("提取的文字超过 64 KiB")
		}
		b.WriteString(text)
	}
	if !utf8.ValidString(b.String()) {
		return "", errors.New("提取的文字不是 UTF-8")
	}
	return strings.TrimSpace(b.String()), nil
}

func pdfPageBudget(contents pdf.Value) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("PDF 页面无法解码：%v", p)
		}
	}()
	streams := []pdf.Value{contents}
	if contents.Kind() == pdf.Array {
		streams = make([]pdf.Value, contents.Len())
		for i := range streams {
			streams[i] = contents.Index(i)
		}
	}
	remaining := int64(4 << 20)
	for _, stream := range streams {
		if stream.Kind() == pdf.Null {
			continue
		}
		if stream.Kind() != pdf.Stream {
			return errors.New("PDF 页面内容格式无效")
		}
		if stream.Key("DecodeParms").Key("Columns").Int64() > remaining {
			return errors.New("PDF 页面内容超过 4 MiB")
		}
		r := stream.Reader()
		n, readErr := io.CopyN(io.Discard, r, remaining+1)
		_ = r.Close()
		if n > remaining {
			return errors.New("PDF 页面内容超过 4 MiB")
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		remaining -= n
	}
	return nil
}

func docxText(path string) (string, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer z.Close()
	for _, file := range z.File {
		if file.Name != "word/document.xml" {
			continue
		}
		if file.UncompressedSize64 > 2<<20 {
			return "", errors.New("Word 文档内容过大")
		}
		r, err := file.Open()
		if err != nil {
			return "", err
		}
		defer r.Close()
		dec := xml.NewDecoder(io.LimitReader(r, 2<<20+1))
		var b strings.Builder
		inText := false
		for {
			tok, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "", err
			}
			switch v := tok.(type) {
			case xml.StartElement:
				switch v.Name.Local {
				case "t":
					inText = true
				case "tab":
					b.WriteByte('\t')
				case "br":
					b.WriteByte('\n')
				}
			case xml.EndElement:
				if v.Name.Local == "t" {
					inText = false
				}
				if v.Name.Local == "p" {
					b.WriteByte('\n')
				}
			case xml.CharData:
				if inText {
					b.Write(v)
				}
			}
			if b.Len() > MaxTextBytes {
				return "", errors.New("提取的文字超过 64 KiB")
			}
		}
		return strings.TrimSpace(b.String()), nil
	}
	return "", errors.New("不是有效的 DOCX 文件")
}

// Content preserves the usual string form for text-only requests. Images use
// the Chat Completions content-parts form accepted by vision-capable models.
func Content(question string, files []Attachment) any {
	var b strings.Builder
	b.WriteString(question)
	var images []Attachment
	for _, f := range files {
		if f.DataURL != "" {
			images = append(images, f)
			continue
		}
		fmt.Fprintf(&b, "\n\n[附件：%s]\n%s\n[/附件]", f.Name, f.Text)
	}
	if len(images) == 0 {
		return b.String()
	}
	parts := []any{}
	for _, f := range images {
		fmt.Fprintf(&b, "\n\n[图片附件：%s]", f.Name)
	}
	parts = append(parts, map[string]any{"type": "text", "text": b.String()})
	for _, f := range images {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": f.DataURL}})
	}
	return parts
}

func Display(question string, files []Attachment) string {
	if len(files) == 0 {
		return question
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return strings.TrimSpace(question) + "\n附件：" + strings.Join(names, "、")
}

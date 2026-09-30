package aiattachment

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTextAndRejectBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capture.log")
	if err := os.WriteFile(path, []byte("RX 01 03\nTX 02 04"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Read(path)
	if err != nil || a.Name != "capture.log" || a.Text != "RX 01 03\nTX 02 04" || a.DataURL != "" {
		t.Fatalf("attachment=%+v err=%v", a, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.log"), []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(filepath.Join(dir, "bad.log")); err == nil {
		t.Fatal("binary content accepted as text")
	}
}

func TestReadWindowsUTF16Log(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serial.log")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 'R', 0, 'X', 0, ' ', 0, '0', 0, '1', 0}, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Read(path)
	if err != nil || a.Text != "RX 01" {
		t.Fatalf("attachment=%+v err=%v", a, err)
	}
}

func TestReadPNGUsesDataURLAndSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frame.png")
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pngBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Read(path)
	if err != nil || a.Name != "frame.png" || !strings.HasPrefix(a.DataURL, "data:image/png;base64,iVBOR") {
		t.Fatalf("attachment=%+v err=%v", a, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.png"), make([]byte, MaxImageBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(filepath.Join(dir, "large.png")); err == nil {
		t.Fatal("oversize image accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.png"), []byte("\x89PNG\r\n\x1a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(filepath.Join(dir, "broken.png")); err == nil {
		t.Fatal("truncated image accepted")
	}
}

func TestReadWebPImage(t *testing.T) {
	// Fixture from golang.org/x/image/testdata (BSD license).
	a, err := Read("testdata/sample.webp")
	if err != nil || !strings.HasPrefix(a.DataURL, "data:image/webp;base64,") {
		t.Fatalf("attachment=%+v err=%v", a, err)
	}
}

func TestReadOversizeImageExplainsPixelLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.png")
	header := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), make([]byte, 13)...)
	binary.BigEndian.PutUint32(header[16:20], 8000)
	binary.BigEndian.PutUint32(header[20:24], 5000)
	header[24], header[25] = 8, 2
	crc := make([]byte, 4)
	binary.BigEndian.PutUint32(crc, crc32.ChecksumIEEE(header[12:29]))
	header = append(header, crc...)
	if err := os.WriteFile(path, header, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "3200 万像素") {
		t.Fatalf("pixel limit error: %v", err)
	}
}

func TestReadDocxExtractsParagraphs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>通信</w:t></w:r><w:r><w:t>日志</w:t></w:r></w:p><w:p><w:r><w:t>CRC 错误</w:t></w:r></w:p></w:body></w:document>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := Read(path)
	if err != nil || a.Text != "通信日志\nCRC 错误" {
		t.Fatalf("attachment=%+v err=%v", a, err)
	}
}

func TestReadPDFExtractsSelectableText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manual.pdf")
	if err := os.WriteFile(path, samplePDF(), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Read(path)
	if err != nil || !strings.Contains(a.Text, "Hello PDF") {
		t.Fatalf("attachment=%+v err=%v", a, err)
	}
}

func TestReadPDFRejectsExcessiveDecodedPageStream(t *testing.T) {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(bytes.Repeat([]byte{' '}, 4<<20+1)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "compressed.pdf")
	if err := os.WriteFile(path, samplePDFWithStream(compressed.Bytes(), true), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "页面内容超过 4 MiB") {
		t.Fatalf("stream limit: %v", err)
	}
}

func TestContentIncludesTextAndImagesWithoutLeakingImageInDisplay(t *testing.T) {
	files := []Attachment{{Name: "capture.log", Text: "RX 01 03"}, {Name: "frame.png", DataURL: "data:image/png;base64,iVBOR"}}
	content := Content("这帧有问题吗？", files)
	b, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var parts []map[string]any
	if err := json.Unmarshal(b, &parts); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0]["type"] != "text" || !strings.Contains(parts[0]["text"].(string), "RX 01 03") || parts[1]["type"] != "image_url" {
		t.Fatalf("parts=%s", b)
	}
	image := parts[1]["image_url"].(map[string]any)
	if image["url"] != "data:image/png;base64,iVBOR" {
		t.Fatalf("image=%v", image)
	}
	if display := Display("这帧有问题吗？", files); !strings.Contains(display, "capture.log") || !strings.Contains(display, "frame.png") || strings.Contains(display, "iVBOR") || strings.Contains(display, "RX 01 03") {
		t.Fatalf("display=%q", display)
	}
}

func TestTurnJSONPreservesTextOnlyCompatibility(t *testing.T) {
	b, err := json.Marshal(Turn{Role: "user", Content: "hello"})
	if err != nil || string(b) != `{"role":"user","content":"hello"}` {
		t.Fatalf("turn=%s err=%v", b, err)
	}
	b, err = json.Marshal(Turn{Role: "user", Content: "分析", Files: []Attachment{{Name: "frame.png", DataURL: "data:image/png;base64,iVBOR"}}})
	if err != nil || !strings.Contains(string(b), `"image_url"`) || !strings.Contains(string(b), "分析") {
		t.Fatalf("multimodal turn=%s err=%v", b, err)
	}
}

func TestValidateTurnsLimitsTextAndImageHistory(t *testing.T) {
	if err := ValidateTurns([]Turn{{Role: "user", Content: "hello", Files: []Attachment{{Name: "a.png", DataURL: "data:image/png;base64,AA=="}}}}); err != nil {
		t.Fatal(err)
	}
	for _, turns := range [][]Turn{
		{},
		{{Role: "tool", Content: "x"}},
		{{Role: "assistant", Files: []Attachment{{Name: "a.png", DataURL: "data:image/png;base64,AA=="}}}},
		{{Role: "user", Files: []Attachment{{Name: "a.txt", Text: strings.Repeat("a", 513<<10)}}}},
		{{Role: "user", Files: []Attachment{{Name: "a.png", DataURL: strings.Repeat("a", 25<<20)}}}},
	} {
		if err := ValidateTurns(turns); err == nil {
			t.Fatalf("accepted invalid turns: %#v", turns[0:])
		}
	}
}

func TestValidateTurnsAllowsSixMaximumAttachments(t *testing.T) {
	texts := make([]Attachment, 6)
	images := make([]Attachment, 6)
	for i := range texts {
		texts[i] = Attachment{Name: fmt.Sprintf("%d.log", i), Text: strings.Repeat("a", MaxTextBytes)}
		images[i] = Attachment{Name: fmt.Sprintf("%d.png", i), DataURL: "data:image/png;base64," + strings.Repeat("A", (MaxImageBytes+2)/3*4)}
	}
	for _, files := range [][]Attachment{texts, images} {
		if err := ValidateTurns([]Turn{{Role: "user", Content: "分析", Files: files}}); err != nil {
			t.Fatalf("six valid attachments rejected: %v", err)
		}
	}
}

func samplePDF() []byte {
	return samplePDFWithStream([]byte("BT /F1 12 Tf 20 100 Td (Hello PDF) Tj ET"), false)
}

func samplePDFWithStream(content []byte, compressed bool) []byte {
	filter := ""
	if compressed {
		filter = " /Filter /FlateDecode"
	}
	objects := []string{
		`<< /Type /Catalog /Pages 2 0 R >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`,
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`,
		fmt.Sprintf("<< /Length %d%s >>\nstream\n%s\nendstream", len(content), filter, content),
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, n := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", n)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}

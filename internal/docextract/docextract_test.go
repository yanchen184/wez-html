package docextract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// buildZip 組一個記憶體內的 zip(docx/pptx 都是 zip 容器)。
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSupported(t *testing.T) {
	for name, want := range map[string]bool{
		"報表.xlsx":   true,
		"會議記錄.DOCX": true, // 大小寫不敏感
		"簡報.pptx":   true,
		"notes.txt": false,
		"page.html": false,
		"noext":     false,
	} {
		if got := Supported(name); got != want {
			t.Errorf("Supported(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestExtractDocx(t *testing.T) {
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>第一段標題</w:t></w:r></w:p>
    <w:p><w:r><w:t>第二段</w:t></w:r><w:r><w:t>接續文字</w:t></w:r></w:p>
    <w:p><w:r><w:t>A</w:t></w:r><w:r><w:tab/></w:r><w:r><w:t>B</w:t></w:r></w:p>
  </w:body>
</w:document>`
	data := buildZip(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/document.xml":   doc,
	})
	got, err := Extract("會議記錄.docx", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "第一段標題") {
		t.Errorf("missing paragraph 1: %q", got)
	}
	// 同段的多個 run 要接在同一行
	if !strings.Contains(got, "第二段接續文字") {
		t.Errorf("runs in same paragraph should join: %q", got)
	}
	// 段落之間要有換行
	if !strings.Contains(got, "第一段標題\n") {
		t.Errorf("paragraphs should be newline-separated: %q", got)
	}
	// tab 要保留成分隔
	if !strings.Contains(got, "A\tB") {
		t.Errorf("w:tab should become tab: %q", got)
	}
}

func TestExtractXlsx(t *testing.T) {
	f := excelize.NewFile()
	// 預設 sheet 改名 + 填資料
	_ = f.SetSheetName("Sheet1", "人員名單")
	_ = f.SetSheetRow("人員名單", "A1", &[]any{"姓名", "部門", "分機"})
	_ = f.SetSheetRow("人員名單", "A2", &[]any{"王小明", "研發", 123})
	_ = f.SetSheetRow("人員名單", "A3", &[]any{"李大華", "業務", 456})
	// 第二個 sheet
	_, _ = f.NewSheet("統計")
	_ = f.SetSheetRow("統計", "A1", &[]any{"月份", "件數"})
	_ = f.SetSheetRow("統計", "A2", &[]any{"一月", 42})
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}

	got, err := Extract("報表.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"人員名單", "統計", "王小明", "研發", "123", "一月", "42", "| 姓名 | 部門 | 分機 |"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestExtractXlsxRowCap(t *testing.T) {
	f := excelize.NewFile()
	for i := 1; i <= maxRowsPerSheet+50; i++ {
		_ = f.SetSheetRow("Sheet1", fmt.Sprintf("A%d", i), &[]any{fmt.Sprintf("row-%d", i)})
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := Extract("big.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, fmt.Sprintf("row-%d", maxRowsPerSheet+1)) {
		t.Error("rows beyond cap should be dropped")
	}
	if !strings.Contains(got, "截斷") {
		t.Error("should note truncation")
	}
}

func TestExtractPptx(t *testing.T) {
	slide := func(text string) string {
		return `<?xml version="1.0"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld><p:spTree><p:sp><p:txBody>
    <a:p><a:r><a:t>` + text + `</a:t></a:r></a:p>
  </p:txBody></p:sp></p:spTree></p:cSld>
</p:sld>`
	}
	// 故意放 10 張,驗 slide2 排在 slide10 前(數字排序,不是字串排序)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
	}
	for i := 1; i <= 10; i++ {
		files[fmt.Sprintf("ppt/slides/slide%d.xml", i)] = slide(fmt.Sprintf("投影片%d", i))
	}
	got, err := Extract("簡報.pptx", buildZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	i2 := strings.Index(got, "投影片2")
	i10 := strings.Index(got, "投影片10")
	if i2 == -1 || i10 == -1 {
		t.Fatalf("missing slides: %q", got)
	}
	if i2 > i10 {
		t.Error("slide2 should come before slide10 (numeric sort)")
	}
}

func TestExtractCorrupt(t *testing.T) {
	if _, err := Extract("bad.docx", []byte("this is not a zip")); err == nil {
		t.Error("corrupt docx should error")
	}
	if _, err := Extract("bad.xlsx", []byte("nope")); err == nil {
		t.Error("corrupt xlsx should error")
	}
}

func TestExtractUnsupported(t *testing.T) {
	if _, err := Extract("x.pdf", []byte("%PDF-")); err == nil {
		t.Error("unsupported ext should error")
	}
}

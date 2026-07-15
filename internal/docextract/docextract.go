// Package docextract 把 Office 文件(docx / xlsx / pptx)抽成純文字或 markdown 表格,
// 給 /api/generate 餵進 LLM prompt 用。
//
// docx / pptx 都是「zip 容器 + OOXML」,文字全在固定路徑的 XML 裡,用 stdlib 解就夠;
// xlsx 的儲存格型別 / 共享字串 / 日期格式水太深,交給 excelize。
package docextract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// maxRowsPerSheet 限制每個 xlsx sheet 餵給 prompt 的列數,防超大報表灌爆。
const maxRowsPerSheet = 200

// Supported 回報這個檔名(依副檔名)是否走 Office 抽取。
func Supported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx", ".xlsx", ".pptx":
		return true
	}
	return false
}

// Extract 依副檔名分流抽文字。回傳的字串給 LLM 讀,不保證格式嚴謹、只求內容齊。
func Extract(name string, data []byte) (string, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return extractDocx(data)
	case ".xlsx":
		return extractXlsx(data)
	case ".pptx":
		return extractPptx(data)
	}
	return "", fmt.Errorf("unsupported document type: %s", filepath.Ext(name))
}

// --- docx ---

func extractDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("not a valid docx: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			return ooxmlText(rc, "t", "p")
		}
	}
	return "", fmt.Errorf("not a valid docx: word/document.xml missing")
}

// --- pptx ---

var slideRe = regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)

func extractPptx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("not a valid pptx: %w", err)
	}
	type slide struct {
		n int
		f *zip.File
	}
	var slides []slide
	for _, f := range zr.File {
		if m := slideRe.FindStringSubmatch(f.Name); m != nil {
			n, _ := strconv.Atoi(m[1])
			slides = append(slides, slide{n, f})
		}
	}
	if len(slides) == 0 {
		return "", fmt.Errorf("not a valid pptx: no slides found")
	}
	sort.Slice(slides, func(i, j int) bool { return slides[i].n < slides[j].n })

	var b strings.Builder
	for _, s := range slides {
		rc, err := s.f.Open()
		if err != nil {
			return "", err
		}
		text, err := ooxmlText(rc, "t", "p")
		rc.Close()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "--- 投影片 %d ---\n%s\n", s.n, strings.TrimSpace(text))
	}
	return b.String(), nil
}

// ooxmlText 走 XML token 流,收 <…:textElem> 裡的文字;
// 每個 <…:paraElem> 結束補換行,<w:tab/> 補 tab、<w:br/> 補換行。
// docx(w:t / w:p)與 pptx(a:t / a:p)共用。
func ooxmlText(r io.Reader, textElem, paraElem string) (string, error) {
	dec := xml.NewDecoder(r)
	var b strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case textElem:
				inText = true
			case "tab":
				b.WriteByte('\t')
			case "br":
				b.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case textElem:
				inText = false
			case paraElem:
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	return b.String(), nil
}

// --- xlsx ---

func extractXlsx(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("not a valid xlsx: %w", err)
	}
	defer f.Close()

	var b strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return "", fmt.Errorf("read sheet %s: %w", sheet, err)
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## Sheet: %s\n\n", sheet)
		truncated := false
		if len(rows) > maxRowsPerSheet {
			rows = rows[:maxRowsPerSheet]
			truncated = true
		}
		width := 0
		for _, row := range rows {
			if len(row) > width {
				width = len(row)
			}
		}
		for i, row := range rows {
			b.WriteString("| ")
			for c := 0; c < width; c++ {
				cell := ""
				if c < len(row) {
					cell = strings.ReplaceAll(row[c], "|", "\\|")
					cell = strings.ReplaceAll(cell, "\n", " ")
				}
				b.WriteString(cell)
				b.WriteString(" | ")
			}
			b.WriteString("\n")
			if i == 0 {
				// markdown 表頭分隔列
				b.WriteString(strings.Repeat("| --- ", width))
				b.WriteString("|\n")
			}
		}
		if truncated {
			fmt.Fprintf(&b, "\n(此 sheet 超過 %d 列,已截斷)\n", maxRowsPerSheet)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("xlsx has no data")
	}
	return b.String(), nil
}

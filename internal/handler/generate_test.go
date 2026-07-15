package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func multipartForm(fields map[string]string) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestSanitizeGeneratedHTML(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		wantOK bool
		starts string
		ends   string
	}{
		{
			name:   "clean doctype",
			in:     "<!doctype html><html><body>hi</body></html>",
			wantOK: true,
			starts: "<!doctype html>",
			ends:   "</html>",
		},
		{
			name:   "leading chatter stripped",
			in:     "Sure! Here is your page:\n<!DOCTYPE html><html></html>\nHope that helps!",
			wantOK: true,
			starts: "<!DOCTYPE html>",
			ends:   "</html>",
		},
		{
			name:   "markdown fence",
			in:     "```html\n<!doctype html><html><body>x</body></html>\n```",
			wantOK: true,
			starts: "<!doctype html>",
			ends:   "</html>",
		},
		{
			name:   "html without doctype",
			in:     "<html><head></head><body>y</body></html>",
			wantOK: true,
			starts: "<html>",
			ends:   "</html>",
		},
		{
			name:   "no html at all",
			in:     "I cannot help with that request.",
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := sanitizeGeneratedHTML(c.in)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (got=%q)", ok, c.wantOK, got)
			}
			if !c.wantOK {
				return
			}
			if !strings.HasPrefix(got, c.starts) {
				t.Errorf("prefix: got %q, want start %q", got, c.starts)
			}
			if !strings.HasSuffix(got, c.ends) {
				t.Errorf("suffix: got %q, want end %q", got, c.ends)
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	p := buildPrompt("make a page", "some doc text")
	if !strings.Contains(p, "make a page") {
		t.Error("missing user request")
	}
	if !strings.Contains(p, "some doc text") {
		t.Error("missing doc text")
	}
	// 無文件時不應出現「參考文件內容」段
	p2 := buildPrompt("make a page", "")
	if strings.Contains(p2, "參考文件內容") {
		t.Error("empty doc should not add doc section")
	}
}

// 上傳壞掉的 Office 檔 → 400,不能默默當純文字餵進 prompt。
func TestGenerateCorruptOfficeDoc(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	s.GenCfg = GenConfig{Enabled: true, ClaudeBin: "/nonexistent/claude", DefaultUser: "ai"}
	mux := http.NewServeMux()
	s.Routes(mux)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("prompt", "做一頁")
	_ = mw.WriteField("site", "doc-test")
	_ = mw.WriteField("identity", "ai")
	fw, _ := mw.CreateFormFile("doc", "report.xlsx")
	_, _ = fw.Write([]byte("this is not a real xlsx"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/generate", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "document") {
		t.Errorf("error should mention document parsing: %s", rec.Body.String())
	}
}

func TestBuildRefinePrompt(t *testing.T) {
	cur := "<!doctype html><html><body>old</body></html>"
	p := buildRefinePrompt("把標題改成紅色", cur)
	if !strings.Contains(p, "把標題改成紅色") {
		t.Error("missing refine instruction")
	}
	if !strings.Contains(p, cur) {
		t.Error("missing current HTML as base")
	}
}

// refine 針對不存在的站台 → 404。
func TestRefineMissingSite(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	s.GenCfg = GenConfig{Enabled: true, ClaudeBin: "/nonexistent/claude", DefaultUser: "ai"}
	mux := http.NewServeMux()
	s.Routes(mux)

	body, ct := multipartForm(map[string]string{
		"prompt":   "改紅色",
		"site":     "ghost",
		"identity": "ai",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/refine", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// refine 只有原上傳者能改(沿用 delete/rename 的擁有者語意)。
func TestRefineWrongUploader(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	s.GenCfg = GenConfig{Enabled: true, ClaudeBin: "/nonexistent/claude", DefaultUser: "ai"}
	mux := http.NewServeMux()
	s.Routes(mux)

	if _, err := s.writeSiteHTML("mine", "bob", "", "generated", "", []byte("<html></html>"), DefaultTTL); err != nil {
		t.Fatal(err)
	}

	body, ct := multipartForm(map[string]string{
		"prompt":   "改紅色",
		"site":     "mine",
		"identity": "eve",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/refine", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// generate 端點在 GenCfg.Enabled=false 時不應掛載 /new、/api/generate。
func TestGenerateDisabledByDefault(t *testing.T) {
	s := New(t.TempDir(), "http://x", nil, "")
	mux := http.NewServeMux()
	s.Routes(mux)

	for _, path := range []string{"/new", "/api/generate"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// /new 未註冊時會落到 "/" root handler(serveSite → 404),
		// 重點是不會 200 回生成頁。
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "AI 生成網站") {
			t.Errorf("%s should not serve generate page when disabled", path)
		}
	}
}

// 站台衝突:已存在且未 force → 409。
func TestGenerateConflict(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	s.GenCfg = GenConfig{Enabled: true, ClaudeBin: "/nonexistent/claude", DefaultUser: "ai"}
	mux := http.NewServeMux()
	s.Routes(mux)

	// 先放一個既有站台
	if _, err := s.writeSiteHTML("taken", "bob", "", "x", "", []byte("<html></html>"), DefaultTTL); err != nil {
		t.Fatal(err)
	}

	body, ct := multipartForm(map[string]string{
		"prompt":   "hi",
		"site":     "taken",
		"identity": "eve",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/generate", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
}

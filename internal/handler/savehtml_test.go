package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedSite 直接落一個站台當測試基底(不走 HTTP,避免依賴 upload 流程)。
func seedSite(t *testing.T, s *Server, site, identity, html string) {
	t.Helper()
	if _, err := s.writeSiteHTML(site, identity, "", "seed", "", []byte(html), DefaultTTL); err != nil {
		t.Fatal(err)
	}
}

// 手動存檔:本人存 → 200 且磁碟上的 index.html 真的變了。
func TestSaveHTML(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	mux := http.NewServeMux()
	s.Routes(mux)
	seedSite(t, s, "mysite", "bob", "<html>v1</html>")

	rec := doJSON(t, mux, "POST", "/api/save-html", map[string]string{
		"site": "mysite", "identity": "bob", "html": "<html>v2</html>",
	})
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "ok" || !strings.Contains(resp.URL, "/mysite/") {
		t.Fatalf("bad response: %s", rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "mysite", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<html>v2</html>" {
		t.Fatalf("index.html not overwritten: %s", got)
	}
}

// 非擁有者存 → 403 且內容不變;不存在的站台 → 404。
func TestSaveHTMLOwnership(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	mux := http.NewServeMux()
	s.Routes(mux)
	seedSite(t, s, "mysite", "bob", "<html>v1</html>")

	rec := doJSON(t, mux, "POST", "/api/save-html", map[string]string{
		"site": "mysite", "identity": "eve", "html": "<html>hacked</html>",
	})
	if rec.Code != 403 {
		t.Errorf("save by other: want 403, got %d (%s)", rec.Code, rec.Body.String())
	}
	got, _ := os.ReadFile(filepath.Join(root, "mysite", "index.html"))
	if string(got) != "<html>v1</html>" {
		t.Errorf("content should be untouched, got %s", got)
	}

	rec = doJSON(t, mux, "POST", "/api/save-html", map[string]string{
		"site": "nosuch", "identity": "bob", "html": "<html>x</html>",
	})
	if rec.Code != 404 {
		t.Errorf("missing site: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// 欄位驗證:壞站名 / 壞 identity / 空 html → 400;GET → 405。
func TestSaveHTMLValidation(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	mux := http.NewServeMux()
	s.Routes(mux)
	seedSite(t, s, "mysite", "bob", "<html>v1</html>")

	cases := []map[string]string{
		{"site": "Bad Site!", "identity": "bob", "html": "<html>x</html>"},
		{"site": "mysite", "identity": "b b", "html": "<html>x</html>"},
		{"site": "mysite", "identity": "bob", "html": "   "},
	}
	for i, c := range cases {
		if rec := doJSON(t, mux, "POST", "/api/save-html", c); rec.Code != 400 {
			t.Errorf("case %d: want 400, got %d (%s)", i, rec.Code, rec.Body.String())
		}
	}
	if rec := doJSON(t, mux, "GET", "/api/save-html", nil); rec.Code != 405 {
		t.Errorf("GET: want 405, got %d", rec.Code)
	}
}

// force 覆蓋語意沿用 writeSiteHTML:存檔後 KV 資料要還在。
func TestSaveHTMLPreservesKV(t *testing.T) {
	root := t.TempDir()
	s := New(root, "http://x", nil, "")
	mux := http.NewServeMux()
	s.Routes(mux)
	seedSite(t, s, "mysite", "bob", "<html>v1</html>")

	// 先塞一筆 KV(走站台 KV API)
	req := doJSON(t, mux, "PUT", "/mysite/api/kv/greet", map[string]string{"msg": "hi"})
	if req.Code != 200 {
		t.Fatalf("kv put: %d (%s)", req.Code, req.Body.String())
	}

	rec := doJSON(t, mux, "POST", "/api/save-html", map[string]string{
		"site": "mysite", "identity": "bob", "html": "<html>v2</html>",
	})
	if rec.Code != 200 {
		t.Fatalf("save: %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, mux, "GET", "/mysite/api/kv/greet", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("kv should survive save: %d (%s)", rec.Code, rec.Body.String())
	}
}

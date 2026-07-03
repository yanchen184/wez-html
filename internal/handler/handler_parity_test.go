package handler

// 這些測試把「wez(Go)與 html.yanchen.app(CF functions)必須一致」的行為契約
// 釘成可執行 spec。CF 端是 Go handler 的移植,兩邊皮/功能要同步,靠這裡擋回歸。
//
// 已知且「刻意不同」的差異在對應測試上用 PARITY-DIFF 註解標明,
// 實機 parity 腳本(scripts/parity-check.sh)會再從外部驗一次真實 HTTP 行為。

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yanchen184/wez-html/internal/kv"
	"github.com/yanchen184/wez-html/internal/meta"
)

// newServer 建一個以 tmpdir 為 root 的 server。
func newServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	return &Server{Root: root, PublicURL: "http://example.test"}, root
}

// uploadSingleReq 組一個 /api/upload-single 的 multipart 請求。
func uploadSingleReq(t *testing.T, fields map[string]string, fileName, fileBody string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if fileName != "" {
		fw, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(fileBody)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/upload-single", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// --- 上傳:成功路徑 -------------------------------------------------

func TestUploadSingle_ok(t *testing.T) {
	s, root := newServer(t)
	req := uploadSingleReq(t, map[string]string{
		"site":         "demo-site",
		"identity":     "yc",
		"project_name": "示範專案",
	}, "page.html", "<h1>hi</h1>")
	rec := httptest.NewRecorder()
	s.uploadSingle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// 落地:site/index.html + meta,project_name 有存。
	if got, err := os.ReadFile(filepath.Join(root, "demo-site", "index.html")); err != nil || string(got) != "<h1>hi</h1>" {
		t.Fatalf("index.html = %q err=%v", got, err)
	}
	m, err := meta.Load(root, "demo-site")
	if err != nil {
		t.Fatal(err)
	}
	if m.ProjectName != "示範專案" || m.Uploader != "yc" || m.Files != 1 {
		t.Fatalf("meta = %#v", m)
	}
}

// --- 上傳:驗證邊界(regex / 副檔名),兩邊回同樣的 400 ---------------

func TestUploadSingle_validation(t *testing.T) {
	cases := []struct {
		name     string
		fields   map[string]string
		fileName string
		want     int
	}{
		{"bad site 大寫", map[string]string{"site": "BadSite", "identity": "yc"}, "a.html", 400},
		{"bad site 空白", map[string]string{"site": "", "identity": "yc"}, "a.html", 400},
		{"bad site 超長", map[string]string{"site": strings.Repeat("a", 41), "identity": "yc"}, "a.html", 400},
		{"bad identity", map[string]string{"site": "ok", "identity": "有中文"}, "a.html", 400},
		{"非 html 副檔名", map[string]string{"site": "ok", "identity": "yc"}, "a.txt", 400},
		{"缺檔案", map[string]string{"site": "ok", "identity": "yc"}, "", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newServer(t)
			req := uploadSingleReq(t, tc.fields, tc.fileName, "<html></html>")
			rec := httptest.NewRecorder()
			s.uploadSingle(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// --- 上傳:撞名回 409 + existing_uploader(不帶 force)-----------------

func TestUploadSingle_conflictWithoutForce(t *testing.T) {
	s, _ := newServer(t)
	base := map[string]string{"site": "dup", "identity": "yc"}
	rec1 := httptest.NewRecorder()
	s.uploadSingle(rec1, uploadSingleReq(t, base, "a.html", "<b>1</b>"))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first upload = %d", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	s.uploadSingle(rec2, uploadSingleReq(t, base, "a.html", "<b>2</b>"))
	if rec2.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body = %s", rec2.Code, rec2.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "conflict" || resp["existing_uploader"] != "yc" {
		t.Fatalf("conflict resp = %#v", resp)
	}
}

// --- 上傳:force 覆蓋、未帶 project_name 時沿用舊值(對齊 CF)------------

func TestUploadSingle_forcePreservesProjectName(t *testing.T) {
	s, root := newServer(t)
	// 先上傳帶 project_name。
	rec1 := httptest.NewRecorder()
	s.uploadSingle(rec1, uploadSingleReq(t, map[string]string{
		"site": "keep-pn", "identity": "yc", "project_name": "原專案名",
	}, "a.html", "<b>v1</b>"))
	if rec1.Code != http.StatusOK {
		t.Fatalf("v1 = %d body=%s", rec1.Code, rec1.Body.String())
	}
	// force 覆蓋、不帶 project_name。
	rec2 := httptest.NewRecorder()
	s.uploadSingle(rec2, uploadSingleReq(t, map[string]string{
		"site": "keep-pn", "identity": "yc", "force": "1",
	}, "a.html", "<b>v2</b>"))
	if rec2.Code != http.StatusOK {
		t.Fatalf("v2 = %d body=%s", rec2.Code, rec2.Body.String())
	}
	m, err := meta.Load(root, "keep-pn")
	if err != nil {
		t.Fatal(err)
	}
	if m.ProjectName != "原專案名" {
		t.Fatalf("force 覆蓋後 project_name 應沿用舊值,got %q", m.ProjectName)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "keep-pn", "index.html")); string(got) != "<b>v2</b>" {
		t.Fatalf("內容應已更新為 v2,got %q", got)
	}
}

// --- 上傳:force 覆蓋保留站台 KV data(對齊 CF 的 data: 前綴保留)---------

func TestUploadSingle_forcePreservesKV(t *testing.T) {
	s, root := newServer(t)
	rec1 := httptest.NewRecorder()
	s.uploadSingle(rec1, uploadSingleReq(t, map[string]string{"site": "kv-keep", "identity": "yc"}, "a.html", "<b>1</b>"))
	if rec1.Code != http.StatusOK {
		t.Fatalf("v1 = %d", rec1.Code)
	}
	// 塞一個 KV data 檔。
	dataDir := filepath.Join(root, "kv-keep", kv.DataDirName)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "state.json"), []byte(`{"n":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// force 覆蓋。
	rec2 := httptest.NewRecorder()
	s.uploadSingle(rec2, uploadSingleReq(t, map[string]string{"site": "kv-keep", "identity": "yc", "force": "1"}, "a.html", "<b>2</b>"))
	if rec2.Code != http.StatusOK {
		t.Fatalf("v2 = %d", rec2.Code)
	}
	if got, err := os.ReadFile(filepath.Join(dataDir, "state.json")); err != nil || string(got) != `{"n":42}` {
		t.Fatalf("force 覆蓋後 KV 應保留,got %q err=%v", got, err)
	}
}

// --- normalizeProjectName:兩邊同規則(trim / 單行 / ≤80 rune)----------

func TestNormalizeProjectName_parity(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"trim", "  hi  ", "hi", false},
		{"空字串", "", "", false},
		{"含換行拒絕", "a\nb", "", true},
		{"含 CR 拒絕", "a\rb", "", true},
		{"剛好 80 rune", strings.Repeat("字", 80), strings.Repeat("字", 80), false},
		{"81 rune 拒絕", strings.Repeat("字", 81), "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeProjectName(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("want err, got nil (%q)", got)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				if got != tc.want {
					t.Fatalf("got %q want %q", got, tc.want)
				}
			}
		})
	}
}

// --- /api/sites:列表結構(前端兩邊 fetch 同一份 shape)-----------------

func TestListSites_shape(t *testing.T) {
	s, _ := newServer(t)
	// 上傳兩站。
	for _, site := range []string{"alpha", "beta"} {
		rec := httptest.NewRecorder()
		s.uploadSingle(rec, uploadSingleReq(t, map[string]string{"site": site, "identity": "yc"}, "a.html", "<b>x</b>"))
		if rec.Code != http.StatusOK {
			t.Fatalf("upload %s = %d", site, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.listSites(rec, httptest.NewRequest(http.MethodGet, "/api/sites", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("listSites = %d", rec.Code)
	}
	var resp struct {
		Sites []siteSummary `json:"sites"`
		Total int           `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 || len(resp.Sites) != 2 {
		t.Fatalf("total=%d sites=%d", resp.Total, len(resp.Sites))
	}
	// 每站要有 name / uploader / url,前端才 render 得出來。
	for _, x := range resp.Sites {
		if x.Name == "" || x.Uploader != "yc" || !strings.HasPrefix(x.URL, "/") {
			t.Fatalf("site summary 缺欄位: %#v", x)
		}
	}
}

// --- 未上傳站台的 site API 一律 404(對齊 CF)--------------------------

func TestSiteAPI_notFound(t *testing.T) {
	s, _ := newServer(t)
	for _, path := range []string{
		"/api/site/nope/rename",
		"/api/site/nope/project-name",
	} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"identity":"yc","new_site":"x","project_name":"y"}`))
		rec := httptest.NewRecorder()
		s.siteAPI(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404; body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

// ensure time import used (meta.Save 的呼叫端在別檔;此檔保留 time 供未來 case)
var _ = time.Now

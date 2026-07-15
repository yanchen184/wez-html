package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTplServer(t *testing.T) *http.ServeMux {
	t.Helper()
	s := New(t.TempDir(), "http://x", nil, "")
	s.GenCfg = GenConfig{Enabled: true, ClaudeBin: "/nonexistent/claude", DefaultUser: "ai"}
	mux := http.NewServeMux()
	s.Routes(mux)
	return mux
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// 完整 CRUD 流程:建 → 列 → 改 → 刪。
func TestTemplateCRUD(t *testing.T) {
	mux := newTplServer(t)

	// create
	rec := doJSON(t, mux, "POST", "/api/templates", map[string]string{
		"name": "值班表", "icon": "📅", "desc": "輪班用",
		"prompt": "做一個值班表頁面", "identity": "bob",
	})
	if rec.Code != 201 {
		t.Fatalf("create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var created Template
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "值班表" || created.Identity != "bob" {
		t.Fatalf("bad created template: %+v", created)
	}

	// list
	rec = doJSON(t, mux, "GET", "/api/templates", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "值班表") {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	// update(本人)
	rec = doJSON(t, mux, "PUT", "/api/templates/"+created.ID, map[string]string{
		"name": "值班表v2", "prompt": "做一個更好的值班表", "identity": "bob",
	})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "值班表v2") {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}

	// delete(本人)
	rec = doJSON(t, mux, "DELETE", "/api/templates/"+created.ID, map[string]string{"identity": "bob"})
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, mux, "GET", "/api/templates", nil)
	if strings.Contains(rec.Body.String(), "值班表") {
		t.Fatalf("template should be gone: %s", rec.Body.String())
	}
}

// 只有建立者能改/刪(403),不存在的 id 是 404。
func TestTemplateOwnership(t *testing.T) {
	mux := newTplServer(t)
	rec := doJSON(t, mux, "POST", "/api/templates", map[string]string{
		"name": "x", "prompt": "p", "identity": "bob",
	})
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var created Template
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	if rec := doJSON(t, mux, "PUT", "/api/templates/"+created.ID, map[string]string{
		"name": "y", "prompt": "p", "identity": "eve",
	}); rec.Code != 403 {
		t.Errorf("update by other: want 403, got %d", rec.Code)
	}
	if rec := doJSON(t, mux, "DELETE", "/api/templates/"+created.ID, map[string]string{"identity": "eve"}); rec.Code != 403 {
		t.Errorf("delete by other: want 403, got %d", rec.Code)
	}
	if rec := doJSON(t, mux, "DELETE", "/api/templates/t999-1", map[string]string{"identity": "bob"}); rec.Code != 404 {
		t.Errorf("delete missing: want 404, got %d", rec.Code)
	}
}

// 欄位驗證:缺 name / 缺 prompt / 壞 identity → 400;icon 空給預設。
func TestTemplateValidation(t *testing.T) {
	mux := newTplServer(t)
	cases := []map[string]string{
		{"prompt": "p", "identity": "bob"},              // no name
		{"name": "x", "identity": "bob"},                // no prompt
		{"name": "x", "prompt": "p", "identity": "b b"}, // bad identity
	}
	for i, c := range cases {
		if rec := doJSON(t, mux, "POST", "/api/templates", c); rec.Code != 400 {
			t.Errorf("case %d: want 400, got %d (%s)", i, rec.Code, rec.Body.String())
		}
	}
	rec := doJSON(t, mux, "POST", "/api/templates", map[string]string{
		"name": "x", "prompt": "p", "identity": "bob",
	})
	var created Template
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Icon != "📄" {
		t.Errorf("empty icon should default to 📄, got %q", created.Icon)
	}
}

// AI 產模板:desc 必填;job 被接受後可輪詢(claude 不存在 → 最終 error,不會 panic)。
func TestTemplateAIAccepted(t *testing.T) {
	mux := newTplServer(t)
	if rec := doJSON(t, mux, "POST", "/api/templates/ai", map[string]string{}); rec.Code != 400 {
		t.Errorf("empty desc: want 400, got %d", rec.Code)
	}
	rec := doJSON(t, mux, "POST", "/api/templates/ai", map[string]string{"desc": "客訴處理進度看板"})
	if rec.Code != 202 {
		t.Fatalf("want 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Job  string `json:"job"`
		Poll string `json:"poll"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Job == "" || !strings.HasPrefix(resp.Poll, "/api/generate/") {
		t.Fatalf("bad accept response: %s", rec.Body.String())
	}
}

func TestParseTplDraft(t *testing.T) {
	good := `{"name":"客訴看板","icon":"📌","desc":"追蹤客訴","prompt":"做一個客訴追蹤頁"}`
	for _, raw := range []string{
		good,
		"好的,這是模板:\n```json\n" + good + "\n```\n希望有幫助!",
		"前言 " + good + " 後記",
	} {
		in, err := parseTplDraft(raw)
		if err != nil {
			t.Errorf("parse %q: %v", raw[:20], err)
			continue
		}
		if in.Name != "客訴看板" || in.Prompt == "" {
			t.Errorf("bad draft: %+v", in)
		}
	}
	for _, raw := range []string{"", "no json here", `{"name":"x"}`} {
		if _, err := parseTplDraft(raw); err == nil {
			t.Errorf("should fail: %q", raw)
		}
	}
}

// 生成 / 修改 prompt 都要教 KV API,生成的頁面才會用站台資料庫。
func TestPromptsIncludeKVDoc(t *testing.T) {
	if p := buildPrompt("留言板", ""); !strings.Contains(p, "api/kv") {
		t.Error("buildPrompt should teach KV API")
	}
	if p := buildRefinePrompt("加留言功能", "<html></html>"); !strings.Contains(p, "api/kv") {
		t.Error("buildRefinePrompt should teach KV API")
	}
}

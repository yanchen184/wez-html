package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yanchen184/wez-html/internal/kv"
	"github.com/yanchen184/wez-html/internal/meta"
)

func TestPreserveKVOnForce_keepsDataDir(t *testing.T) {
	tmp := t.TempDir()
	siteDir := filepath.Join(tmp, "mysite")
	staging := filepath.Join(tmp, "mysite.staging")

	if err := os.MkdirAll(filepath.Join(siteDir, kv.DataDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, kv.DataDirName, "feedback.json"), []byte(`{"items":[1,2,3]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "index.html"), []byte("<html>new</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	preserveKVOnForce(siteDir, staging)
	_ = os.RemoveAll(siteDir)
	if err := os.Rename(staging, siteDir); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(siteDir, kv.DataDirName, "feedback.json"))
	if err != nil {
		t.Fatalf("KV 檔被砍了: %v", err)
	}
	if string(got) != `{"items":[1,2,3]}` {
		t.Fatalf("KV 內容變了: %s", got)
	}
}

func TestPreserveKVOnForce_noOpWhenNoDataDir(t *testing.T) {
	tmp := t.TempDir()
	siteDir := filepath.Join(tmp, "fresh")
	staging := filepath.Join(tmp, "fresh.staging")

	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}

	preserveKVOnForce(siteDir, staging)

	if _, err := os.Stat(filepath.Join(staging, kv.DataDirName)); !os.IsNotExist(err) {
		t.Fatalf("空站台不該在 staging 蓋出 .data: %v", err)
	}
}

func TestRenameSite_movesFilesKVAndMetadata(t *testing.T) {
	root := t.TempDir()
	oldSite := "old-site"
	newSite := "new-site"
	oldDir := filepath.Join(root, oldSite)
	if err := os.MkdirAll(filepath.Join(oldDir, kv.DataDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "index.html"), []byte("<h1>site</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, kv.DataDirName, "feedback.json"), []byte(`{"score":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := meta.Save(root, &meta.Meta{Site: oldSite, Uploader: "yc", UploadedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"identity":"yc","new_site":"new-site"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/site/old-site/rename", body)
	rec := httptest.NewRecorder()
	server := &Server{Root: root, PublicURL: "http://example.test"}
	server.renameSite(rec, req, oldSite)

	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["site"] != newSite {
		t.Fatalf("response site = %v, want %s", response["site"], newSite)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old site directory still exists: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, newSite, kv.DataDirName, "feedback.json")); err != nil || string(got) != `{"score":1}` {
		t.Fatalf("KV was not preserved: %q, %v", got, err)
	}
	renamedMeta, err := meta.Load(root, newSite)
	if err != nil {
		t.Fatal(err)
	}
	if renamedMeta.Site != newSite || renamedMeta.Uploader != "yc" {
		t.Fatalf("renamed metadata = %#v", renamedMeta)
	}
}

func TestRenameSite_rejectsExistingNameAndWrongUploader(t *testing.T) {
	root := t.TempDir()
	for _, site := range []string{"source", "taken"} {
		if err := os.MkdirAll(filepath.Join(root, site), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := meta.Save(root, &meta.Meta{Site: site, Uploader: "yc", UploadedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	server := &Server{Root: root, PublicURL: "http://example.test"}

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{name: "taken name", body: `{"identity":"yc","new_site":"taken"}`, want: http.StatusConflict},
		{name: "wrong uploader", body: `{"identity":"other","new_site":"fresh"}`, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/site/source/rename", bytes.NewBufferString(tc.body))
			rec := httptest.NewRecorder()
			server.renameSite(rec, req, "source")
			if rec.Code != tc.want {
				t.Fatalf("rename status = %d, want %d; body = %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestUpdateProjectName_preservesSiteURL(t *testing.T) {
	root := t.TempDir()
	site := "customer-demo"
	if err := os.MkdirAll(filepath.Join(root, site), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := meta.Save(root, &meta.Meta{Site: site, Uploader: "yc", UploadedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	server := &Server{Root: root, PublicURL: "http://example.test"}
	req := httptest.NewRequest(http.MethodPost, "/api/site/customer-demo/project-name", bytes.NewBufferString(`{"identity":"yc","project_name":"六月客戶提案"}`))
	rec := httptest.NewRecorder()
	server.updateProjectName(rec, req, site)
	if rec.Code != http.StatusOK {
		t.Fatalf("project name status = %d, body = %s", rec.Code, rec.Body.String())
	}

	m, err := meta.Load(root, site)
	if err != nil {
		t.Fatal(err)
	}
	if m.ProjectName != "六月客戶提案" || m.Site != site {
		t.Fatalf("metadata = %#v", m)
	}
	if _, err := os.Stat(filepath.Join(root, site)); err != nil {
		t.Fatalf("site directory changed: %v", err)
	}
}

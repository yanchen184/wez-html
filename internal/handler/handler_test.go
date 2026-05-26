package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yanchen184/wez-html/internal/kv"
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

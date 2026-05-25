package reaper

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yanchen184/wez-html/internal/meta"
)

func Run(ctx context.Context, root string, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	sweep(root)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep(root)
		}
	}
}

func sweep(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		log.Printf("reaper: readdir %s: %v", root, err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		site := e.Name()
		m, err := meta.Load(root, site)
		if err != nil {
			log.Printf("reaper: skip %s (no meta or corrupt): %v", site, err)
			continue
		}
		if m.Expired() {
			path := filepath.Join(root, site)
			if err := os.RemoveAll(path); err != nil {
				log.Printf("reaper: rm %s: %v", path, err)
				continue
			}
			log.Printf("reaper: removed expired site %s (uploader=%s expired=%s)",
				site, m.Uploader, m.ExpiresAt.Format(time.RFC3339))
		}
	}
}

package meta

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const FileName = ".meta.json"

type Meta struct {
	Site       string    `json:"site"`
	Uploader   string    `json:"uploader"`
	UploadedAt time.Time `json:"uploaded_at"`
	TTLDays    int       `json:"ttl_days"`
	ExpiresAt  time.Time `json:"expires_at"`
	Src        string    `json:"src,omitempty"`
	SizeBytes  int64     `json:"size_bytes"`
	Files      int       `json:"files"`
}

func Path(root, site string) string {
	return filepath.Join(root, site, FileName)
}

func Load(root, site string) (*Meta, error) {
	b, err := os.ReadFile(Path(root, site))
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse meta: %w", err)
	}
	return &m, nil
}

func Save(root string, m *Meta) error {
	dir := filepath.Join(root, m.Site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, FileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, FileName))
}

func (m *Meta) DaysLeft() int {
	d := time.Until(m.ExpiresAt) / (24 * time.Hour)
	if d < 0 {
		return 0
	}
	return int(d)
}

func (m *Meta) Expired() bool {
	return time.Now().After(m.ExpiresAt)
}

func ComputeExpiresAt(ttlDays int) time.Time {
	return time.Now().Add(time.Duration(ttlDays) * 24 * time.Hour)
}

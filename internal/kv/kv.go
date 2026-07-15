// Package kv: 給每個站台一份「最陽春」的 JSON key-value store。
//
// 設計刻意走「夠用就好」:
//   - 一個 key 一個檔(<siteDir>/.data/<key>.json)
//   - value 必須是合法 JSON(server 端不解析,只 Validate + 原樣存)
//   - 沒 transaction、沒 schema、沒 query;dev 自己保證 race 安全
//   - 跟 site 共生死:site 過期 reaper 刪整個 siteDir,KV 跟著走
//
// 真要做關聯查詢、index、SQL → 等 v2 Datasette。
package kv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	DataDirName  = ".data"
	MaxValueSize = 256 * 1024       // 256KB / value
	MaxKeys      = 1000             // 1000 keys / site
	MaxTotalSize = 10 * 1024 * 1024 // 10MB / site
)

var (
	KeyRe          = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	ErrBadKey      = errors.New("key must match ^[a-zA-Z0-9_-]{1,64}$")
	ErrNotFound    = errors.New("key not found")
	ErrValueTooBig = fmt.Errorf("value exceeds %d bytes", MaxValueSize)
	ErrTooManyKeys = fmt.Errorf("site exceeds %d keys", MaxKeys)
	ErrSiteFull    = fmt.Errorf("site exceeds %d bytes total", MaxTotalSize)
	ErrBadJSON     = errors.New("value must be valid JSON")
)

type Entry struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
}

func dataDir(siteDir string) string {
	return filepath.Join(siteDir, DataDirName)
}

func keyPath(siteDir, key string) string {
	return filepath.Join(dataDir(siteDir), key+".json")
}

// List 回該站台所有 key + size。沒 `.data/` 回空 slice。
func List(siteDir string) ([]Entry, error) {
	dd := dataDir(siteDir)
	entries, err := os.ReadDir(dd)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Key:       strings.TrimSuffix(name, ".json"),
			SizeBytes: info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Get 讀一個 key,回原始 JSON bytes。
func Get(siteDir, key string) ([]byte, error) {
	if !KeyRe.MatchString(key) {
		return nil, ErrBadKey
	}
	b, err := os.ReadFile(keyPath(siteDir, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return b, nil
}

// Put 寫一個 key。body 必須是合法 JSON。
// 會做:size limit、總站 limit、JSON validate、atomic rename。
func Put(siteDir, key string, r io.Reader) (int64, error) {
	if !KeyRe.MatchString(key) {
		return 0, ErrBadKey
	}
	// limit reader,防 client 灌大檔
	lr := &io.LimitedReader{R: r, N: MaxValueSize + 1}
	buf, err := io.ReadAll(lr)
	if err != nil {
		return 0, err
	}
	if int64(len(buf)) > MaxValueSize {
		return 0, ErrValueTooBig
	}
	// validate JSON
	if !json.Valid(buf) {
		return 0, ErrBadJSON
	}
	// 站台容量檢查(只在「新增 key」或「value 變大」時嚴格擋)
	dd := dataDir(siteDir)
	if err := os.MkdirAll(dd, 0o755); err != nil {
		return 0, err
	}
	existing, _ := List(siteDir)
	var (
		totalNow int64
		hasKey   bool
		oldSize  int64
	)
	for _, e := range existing {
		totalNow += e.SizeBytes
		if e.Key == key {
			hasKey = true
			oldSize = e.SizeBytes
		}
	}
	if !hasKey && len(existing) >= MaxKeys {
		return 0, ErrTooManyKeys
	}
	projected := totalNow - oldSize + int64(len(buf))
	if projected > MaxTotalSize {
		return 0, ErrSiteFull
	}
	// atomic write
	target := keyPath(siteDir, key)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return int64(len(buf)), nil
}

// Delete 刪一個 key。找不到回 ErrNotFound。
func Delete(siteDir, key string) error {
	if !KeyRe.MatchString(key) {
		return ErrBadKey
	}
	err := os.Remove(keyPath(siteDir, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// TotalSize 給 collectSites 用,把 .data/ 也算進該站總用量。
func TotalSize(siteDir string) int64 {
	entries, err := List(siteDir)
	if err != nil {
		return 0
	}
	var total int64
	for _, e := range entries {
		total += e.SizeBytes
	}
	return total
}

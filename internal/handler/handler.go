package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yanchen184/wez-html/internal/archive"
	"github.com/yanchen184/wez-html/internal/kv"
	"github.com/yanchen184/wez-html/internal/meta"
)

const (
	MinTTL = 1
	MaxTTL = 180
)

var (
	siteRe     = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	identityRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,20}$`)
)

type Server struct {
	Root       string
	PublicURL  string
	IndexTmpl  *template.Template
	UploadHTML string
}

func New(root, publicURL string, indexTmpl *template.Template, uploadHTML string) *Server {
	return &Server{Root: root, PublicURL: publicURL, IndexTmpl: indexTmpl, UploadHTML: uploadHTML}
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/upload", s.upload)
	mux.HandleFunc("/api/upload-single", s.uploadSingle)
	mux.HandleFunc("/api/sites", s.listSites)
	mux.HandleFunc("/api/site/", s.siteAPI)
	mux.HandleFunc("/", s.root)
}

// /api/site/<name>            DELETE
// /api/site/<name>/extend     POST
func (s *Server) siteAPI(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/site/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	site := parts[0]
	if !siteRe.MatchString(site) {
		writeErr(w, 400, "bad site name")
		return
	}
	if len(parts) == 1 && r.Method == http.MethodDelete {
		s.deleteSite(w, r, site)
		return
	}
	if len(parts) == 2 && parts[1] == "extend" && r.Method == http.MethodPost {
		s.extendSite(w, r, site)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	if err := r.ParseMultipartForm(int64(archive.MaxTotalSize + 16*1024*1024)); err != nil {
		writeErr(w, 400, fmt.Sprintf("parse form: %v", err))
		return
	}
	site := strings.TrimSpace(r.FormValue("site"))
	identity := strings.TrimSpace(r.FormValue("identity"))
	ttlStr := r.FormValue("ttl")
	force := r.FormValue("force") == "1"

	if !siteRe.MatchString(site) {
		writeErr(w, 400, "site must match ^[a-z0-9-]{1,40}$")
		return
	}
	if !identityRe.MatchString(identity) {
		writeErr(w, 400, "identity must match ^[a-zA-Z0-9_-]{1,20}$")
		return
	}
	ttl, err := strconv.Atoi(ttlStr)
	if err != nil || ttl < MinTTL || ttl > MaxTTL {
		writeErr(w, 400, fmt.Sprintf("ttl must be %d-%d", MinTTL, MaxTTL))
		return
	}

	file, _, err := r.FormFile("archive")
	if err != nil {
		writeErr(w, 400, "missing archive")
		return
	}
	defer file.Close()

	siteDir := filepath.Join(s.Root, site)
	if _, err := os.Stat(siteDir); err == nil {
		if !force {
			existing, _ := meta.Load(s.Root, site)
			resp := map[string]any{
				"status": "conflict",
				"site":   site,
				"hint":   "use --force to overwrite, or --name to rename",
			}
			if existing != nil {
				resp["existing_uploader"] = existing.Uploader
				resp["existing_expires_at"] = existing.ExpiresAt
			}
			writeJSON(w, http.StatusConflict, resp)
			return
		}
	}

	staging := siteDir + ".staging"
	_ = os.RemoveAll(staging)
	st, err := archive.Unpack(file, staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, 400, fmt.Sprintf("unpack: %v", err))
		return
	}

	src := r.FormValue("src")
	now := time.Now()
	m := &meta.Meta{
		Site:       site,
		Uploader:   identity,
		UploadedAt: now,
		TTLDays:    ttl,
		ExpiresAt:  meta.ComputeExpiresAt(ttl),
		Src:        src,
		SizeBytes:  st.SizeBytes,
		Files:      st.Files,
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, meta.FileName), mb, 0o644); err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, 500, fmt.Sprintf("write meta: %v", err))
		return
	}

	preserveKVOnForce(siteDir, staging)
	_ = os.RemoveAll(siteDir)
	if err := os.Rename(staging, siteDir); err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, 500, fmt.Sprintf("rename: %v", err))
		return
	}

	log.Printf("upload: site=%s uploader=%s files=%d size=%d ttl=%dd", site, identity, st.Files, st.SizeBytes, ttl)
	writeJSON(w, 200, map[string]any{
		"status":     "ok",
		"site":       site,
		"url":        fmt.Sprintf("%s/%s/", s.PublicURL, site),
		"uploader":   identity,
		"expires_at": m.ExpiresAt,
		"size_bytes": st.SizeBytes,
		"files":      st.Files,
	})
}

// /api/upload-single — 拖一個 .html 進來,server 自動包成 <site>/index.html。
// 給 web UI 用;CLI 走 /api/upload(本機已先包好 tar.gz)。
func (s *Server) uploadSingle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	if err := r.ParseMultipartForm(int64(archive.MaxFileSize + 4*1024*1024)); err != nil {
		writeErr(w, 400, fmt.Sprintf("parse form: %v", err))
		return
	}
	site := strings.TrimSpace(r.FormValue("site"))
	identity := strings.TrimSpace(r.FormValue("identity"))
	ttlStr := r.FormValue("ttl")
	force := r.FormValue("force") == "1"

	if !siteRe.MatchString(site) {
		writeErr(w, 400, "site must match ^[a-z0-9-]{1,40}$")
		return
	}
	if !identityRe.MatchString(identity) {
		writeErr(w, 400, "identity must match ^[a-zA-Z0-9_-]{1,20}$")
		return
	}
	ttl, err := strconv.Atoi(ttlStr)
	if err != nil || ttl < MinTTL || ttl > MaxTTL {
		writeErr(w, 400, fmt.Sprintf("ttl must be %d-%d", MinTTL, MaxTTL))
		return
	}

	file, fh, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "missing file")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if ext != ".html" && ext != ".htm" {
		writeErr(w, 400, "only .html / .htm allowed in single-file mode")
		return
	}
	if fh.Size > archive.MaxFileSize {
		writeErr(w, 400, fmt.Sprintf("file too big (max %dMB)", archive.MaxFileSize/1024/1024))
		return
	}

	siteDir := filepath.Join(s.Root, site)
	if _, err := os.Stat(siteDir); err == nil {
		if !force {
			existing, _ := meta.Load(s.Root, site)
			resp := map[string]any{
				"status": "conflict",
				"site":   site,
				"hint":   "use force=1 to overwrite, or pick another site name",
			}
			if existing != nil {
				resp["existing_uploader"] = existing.Uploader
				resp["existing_expires_at"] = existing.ExpiresAt
			}
			writeJSON(w, http.StatusConflict, resp)
			return
		}
	}

	staging := siteDir + ".staging"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		writeErr(w, 500, fmt.Sprintf("mkdir: %v", err))
		return
	}
	dst, err := os.Create(filepath.Join(staging, "index.html"))
	if err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, 500, fmt.Sprintf("create: %v", err))
		return
	}
	n, err := io.Copy(dst, file)
	dst.Close()
	if err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, 500, fmt.Sprintf("write: %v", err))
		return
	}

	now := time.Now()
	m := &meta.Meta{
		Site:       site,
		Uploader:   identity,
		UploadedAt: now,
		TTLDays:    ttl,
		ExpiresAt:  meta.ComputeExpiresAt(ttl),
		Src:        fh.Filename,
		SizeBytes:  n,
		Files:      1,
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, meta.FileName), mb, 0o644); err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, 500, fmt.Sprintf("write meta: %v", err))
		return
	}

	preserveKVOnForce(siteDir, staging)
	_ = os.RemoveAll(siteDir)
	if err := os.Rename(staging, siteDir); err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, 500, fmt.Sprintf("rename: %v", err))
		return
	}

	log.Printf("upload-single: site=%s uploader=%s size=%d ttl=%dd", site, identity, n, ttl)
	writeJSON(w, 200, map[string]any{
		"status":     "ok",
		"site":       site,
		"url":        fmt.Sprintf("%s/%s/", s.PublicURL, site),
		"uploader":   identity,
		"expires_at": m.ExpiresAt,
		"size_bytes": n,
		"files":      1,
	})
}

func (s *Server) deleteSite(w http.ResponseWriter, r *http.Request, site string) {
	var body struct {
		Identity string `json:"identity"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	body.Identity = strings.TrimSpace(body.Identity)
	if !identityRe.MatchString(body.Identity) {
		writeErr(w, 400, "bad identity")
		return
	}
	m, err := meta.Load(s.Root, site)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, 404, "not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if m.Uploader != body.Identity {
		writeErr(w, 403, fmt.Sprintf("only original uploader (%s) can delete", m.Uploader))
		return
	}
	if err := os.RemoveAll(filepath.Join(s.Root, site)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("delete: site=%s by=%s", site, body.Identity)
	writeJSON(w, 200, map[string]any{"status": "deleted", "site": site})
}

func (s *Server) extendSite(w http.ResponseWriter, r *http.Request, site string) {
	var body struct {
		Identity string `json:"identity"`
		TTL      int    `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	body.Identity = strings.TrimSpace(body.Identity)
	if !identityRe.MatchString(body.Identity) {
		writeErr(w, 400, "bad identity")
		return
	}
	if body.TTL < MinTTL || body.TTL > MaxTTL {
		writeErr(w, 400, fmt.Sprintf("ttl %d-%d", MinTTL, MaxTTL))
		return
	}
	m, err := meta.Load(s.Root, site)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, 404, "not found")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if m.Uploader != body.Identity {
		writeErr(w, 403, fmt.Sprintf("only original uploader (%s) can extend", m.Uploader))
		return
	}
	m.TTLDays = body.TTL
	m.ExpiresAt = meta.ComputeExpiresAt(body.TTL)
	if err := meta.Save(s.Root, m); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("extend: site=%s by=%s new_ttl=%d", site, body.Identity, body.TTL)
	writeJSON(w, 200, map[string]any{
		"status":     "ok",
		"site":       site,
		"expires_at": m.ExpiresAt,
	})
}

// siteKV: 站台級 KV CRUD。
//   subpath="" 或 "/"           → GET list
//   subpath="/<key>"            → GET / PUT / DELETE
func (s *Server) siteKV(w http.ResponseWriter, r *http.Request, site, siteDir, subpath string) {
	subpath = strings.TrimPrefix(subpath, "/")
	// CORS for browser fetch from same origin (內網信任,放寬一點方便嵌入別處)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}

	// LIST: GET /<site>/api/kv  /  GET /<site>/api/kv/
	if subpath == "" {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "use GET to list keys")
			return
		}
		entries, err := kv.List(siteDir)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		var total int64
		for _, e := range entries {
			total += e.SizeBytes
		}
		writeJSON(w, 200, map[string]any{
			"site":             site,
			"keys":             entries,
			"count":            len(entries),
			"total_size_bytes": total,
			"limits": map[string]any{
				"max_value_size_bytes": kv.MaxValueSize,
				"max_keys":             kv.MaxKeys,
				"max_total_size_bytes": kv.MaxTotalSize,
			},
		})
		return
	}

	// 不允許巢狀 path
	if strings.Contains(subpath, "/") {
		writeErr(w, 400, "nested keys not supported (use flat key names)")
		return
	}
	key := subpath
	if !kv.KeyRe.MatchString(key) {
		writeErr(w, 400, "key must match ^[a-zA-Z0-9_-]{1,64}$")
		return
	}

	switch r.Method {
	case http.MethodGet:
		b, err := kv.Get(siteDir, key)
		if err != nil {
			if errors.Is(err, kv.ErrNotFound) {
				writeErr(w, 404, "key not found")
				return
			}
			writeErr(w, 500, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(b)
	case http.MethodPut:
		n, err := kv.Put(siteDir, key, r.Body)
		if err != nil {
			switch {
			case errors.Is(err, kv.ErrBadJSON):
				writeErr(w, 400, "body must be valid JSON")
			case errors.Is(err, kv.ErrValueTooBig):
				writeErr(w, 413, fmt.Sprintf("value too big (max %d bytes)", kv.MaxValueSize))
			case errors.Is(err, kv.ErrTooManyKeys):
				writeErr(w, 409, fmt.Sprintf("site exceeded %d keys", kv.MaxKeys))
			case errors.Is(err, kv.ErrSiteFull):
				writeErr(w, 409, fmt.Sprintf("site exceeded %d bytes total", kv.MaxTotalSize))
			default:
				writeErr(w, 500, err.Error())
			}
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok", "key": key, "size_bytes": n})
	case http.MethodDelete:
		if err := kv.Delete(siteDir, key); err != nil {
			if errors.Is(err, kv.ErrNotFound) {
				writeErr(w, 404, "key not found")
				return
			}
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"status": "deleted", "key": key})
	default:
		writeErr(w, 405, "method not allowed (GET/PUT/DELETE only)")
	}
}

type siteSummary struct {
	Name       string    `json:"name"`
	Uploader   string    `json:"uploader"`
	UploadedAt time.Time `json:"uploaded_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	DaysLeft   int       `json:"days_left"`
	SizeBytes  int64     `json:"size_bytes"`
	SizeHuman  string    `json:"size_human"`
	URL        string    `json:"url"`
}

func (s *Server) collectSites() []siteSummary {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil
	}
	var out []siteSummary
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		m, err := meta.Load(s.Root, e.Name())
		if err != nil {
			continue
		}
		total := m.SizeBytes + kv.TotalSize(filepath.Join(s.Root, e.Name()))
		out = append(out, siteSummary{
			Name:       m.Site,
			Uploader:   m.Uploader,
			UploadedAt: m.UploadedAt,
			ExpiresAt:  m.ExpiresAt,
			DaysLeft:   m.DaysLeft(),
			SizeBytes:  total,
			SizeHuman:  humanize(total),
			URL:        "/" + m.Site + "/",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UploadedAt.After(out[j].UploadedAt)
	})
	return out
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	sites := s.collectSites()
	var total int64
	for _, x := range sites {
		total += x.SizeBytes
	}
	writeJSON(w, 200, map[string]any{
		"sites":            sites,
		"total":            len(sites),
		"total_size_bytes": total,
	})
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		s.indexPage(w, r)
		return
	}
	s.serveSite(w, r)
}

func (s *Server) indexPage(w http.ResponseWriter, r *http.Request) {
	sites := s.collectSites()
	var total int64
	for _, x := range sites {
		total += x.SizeBytes
	}
	data := map[string]any{
		"Sites":      sites,
		"Total":      len(sites),
		"TotalSize":  humanize(total),
		"GeneratedAt": time.Now().Format("2006-01-02 15:04:05"),
		"Version":    "v1.0.0",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.IndexTmpl.Execute(w, data); err != nil {
		log.Printf("index render: %v", err)
	}
}

func (s *Server) serveSite(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	site := parts[0]
	if !siteRe.MatchString(site) {
		http.NotFound(w, r)
		return
	}
	siteDir := filepath.Join(s.Root, site)
	if _, err := os.Stat(siteDir); err != nil {
		http.NotFound(w, r)
		return
	}
	// 路徑沒 trailing slash 而且是 site root → 301 加 /
	if r.URL.Path == "/"+site {
		http.Redirect(w, r, "/"+site+"/", http.StatusMovedPermanently)
		return
	}
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}
	// /<site>/api/kv/...  → 站台級 KV CRUD (v1.1)
	if rest == "api/kv" || rest == "api/kv/" || strings.HasPrefix(rest, "api/kv/") {
		s.siteKV(w, r, site, siteDir, strings.TrimPrefix(rest, "api/kv"))
		return
	}
	// 其他 /<site>/api/* 預留給未來(v2 Datasette 之類)
	if strings.HasPrefix(rest, "api/") || rest == "api" {
		http.NotFound(w, r)
		return
	}
	// .data/ 內部不對外
	if rest == kv.DataDirName || strings.HasPrefix(rest, kv.DataDirName+"/") {
		http.NotFound(w, r)
		return
	}
	// 內部 meta 不對外
	if rest == meta.FileName {
		http.NotFound(w, r)
		return
	}
	if rest == "" {
		rest = "index.html"
	}
	target := filepath.Join(siteDir, filepath.FromSlash(rest))
	if !strings.HasPrefix(target, siteDir+string(os.PathSeparator)) && target != siteDir {
		http.NotFound(w, r)
		return
	}
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		http.ServeFile(w, r, target)
		return
	}
	// SPA fallback:不是 asset 就回 index.html
	ext := strings.ToLower(filepath.Ext(rest))
	if !isAsset(ext) {
		fallback := filepath.Join(siteDir, "index.html")
		if _, err := os.Stat(fallback); err == nil {
			http.ServeFile(w, r, fallback)
			return
		}
	}
	http.NotFound(w, r)
}

func isAsset(ext string) bool {
	switch ext {
	case ".js", ".mjs", ".css", ".map",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".json", ".xml", ".txt", ".md",
		".mp3", ".mp4", ".webm", ".ogg", ".wav",
		".pdf", ".zip":
		return true
	}
	return false
}

func humanize(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%dB", n)
	}
	u := []string{"K", "M", "G", "T"}
	x := float64(n)
	i := -1
	for x >= k && i < len(u)-1 {
		x /= k
		i++
	}
	return fmt.Sprintf("%.1f%s", x, u[i])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// 確保 io 用到(實際呼叫上面已用)
var _ = io.Copy

// preserveKVOnForce 在 force 覆蓋舊站時保留 KV 資料目錄。
//
// 原本流程:RemoveAll(siteDir) → Rename(staging, siteDir),會把 KV 一起砍掉。
// 修法:Rename 前先把舊 siteDir/.data/ 搬到 staging/.data/,新 site 上線後 KV 還在。
// 任何步驟失敗都回退到原本流程(寧可砍 KV 也不要讓上傳整個失敗)。
//
// 注意:用 Rename 而非 Copy — 同檔案系統下 Rename 是 atomic,且 KV 大檔不會被複製兩次。
// 若搬不動(跨檔案系統等罕見情況),fallback 走 RemoveAll 維持原行為。
func preserveKVOnForce(siteDir, staging string) {
	srcKV := filepath.Join(siteDir, kv.DataDirName)
	dstKV := filepath.Join(staging, kv.DataDirName)
	info, err := os.Stat(srcKV)
	if err != nil || !info.IsDir() {
		return
	}
	if err := os.Rename(srcKV, dstKV); err != nil {
		log.Printf("preserveKVOnForce: rename %s -> %s failed: %v (KV will be lost on this force upload)", srcKV, dstKV, err)
	}
}

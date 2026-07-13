package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yanchen184/wez-html/internal/meta"
)

// generate.go — 「打提示詞 / 丟文件 → claude -p 生 HTML → 落站」的非同步端點。
//
// 為什麼非同步:claude -p 首次生成常需 60-120 秒,遠超一般 HTTP timeout。
// 流程:POST /api/generate 立刻回 job id → 背景 goroutine 跑 claude → GET /api/generate/<job> 輪詢。
//
// 資安邊界(內網工具,信任邊界鬆,但仍要擋住明顯濫用):
//   - claude 一律 --allowedTools ""(禁所有工具)+ --permission-mode bypassPermissions(不停在授權)
//   - prompt 長度上限,擋超大 payload
//   - 生成 timeout(context),避免 goroutine 卡死
//   - 同時生成數上限,避免有人狂點打爆本機 claude
//   - 站台名 / identity 沿用既有 regex 驗證

const (
	maxPromptRunes   = 8000            // prompt + 文件內容合計上限
	maxDocRunes      = 30000           // 上傳文件內容上限(會截斷)
	genTimeout       = 3 * time.Minute // 單次生成上限
	maxConcurrentGen = 3               // 同時生成數
	genJobTTL        = 30 * time.Minute
)

type genJob struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // pending | running | done | error
	Site      string    `json:"site"`
	URL       string    `json:"url,omitempty"`
	Error     string    `json:"error,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// GenConfig 由 main 注入,讓沒裝 claude 的環境能停用端點、測試不會真的 exec。
type GenConfig struct {
	Enabled     bool
	ClaudeBin   string // e.g. /usr/local/bin/claude
	DefaultUser string // 生成站台的預設 uploader identity
}

type genState struct {
	cfg  GenConfig
	mu   sync.Mutex
	jobs map[string]*genJob
	sem  chan struct{}
}

func newGenState(cfg GenConfig) *genState {
	return &genState{
		cfg:  cfg,
		jobs: make(map[string]*genJob),
		sem:  make(chan struct{}, maxConcurrentGen),
	}
}

var htmlBlockRe = regexp.MustCompile(`(?is)<!doctype html.*?</html>`)

// sanitizeGeneratedHTML 從 claude 輸出裡抽出乾淨的 HTML。
// claude -p 有時會夾雜前言 / markdown 圍欄,只認 <!doctype html> … </html>。
func sanitizeGeneratedHTML(raw string) (string, bool) {
	// 先剝 markdown 圍欄(```html ... ```)
	if m := regexp.MustCompile("(?is)```(?:html)?\\s*(.*?)```").FindStringSubmatch(raw); m != nil {
		raw = m[1]
	}
	if m := htmlBlockRe.FindString(raw); m != "" {
		return strings.TrimSpace(m), true
	}
	// 沒有 doctype 但看起來是 html 片段 → 包一層
	if strings.Contains(strings.ToLower(raw), "<html") && strings.Contains(strings.ToLower(raw), "</html>") {
		return strings.TrimSpace(raw), true
	}
	return "", false
}

// genID 產生一個不依賴外部隨機源的 job id(用時間 + 計數)。
var genCounter struct {
	sync.Mutex
	n int
}

func genID() string {
	genCounter.Lock()
	genCounter.n++
	n := genCounter.n
	genCounter.Unlock()
	return fmt.Sprintf("g%d-%d", time.Now().UnixNano(), n)
}

// registerGenerate 把生成端點掛到 mux(main 在 Enabled 時呼叫)。
func (s *Server) registerGenerate(mux *http.ServeMux, cfg GenConfig) {
	gs := newGenState(cfg)
	s.gen = gs
	mux.HandleFunc("/api/generate", func(w http.ResponseWriter, r *http.Request) {
		s.generateStart(w, r, gs)
	})
	mux.HandleFunc("/api/generate/", func(w http.ResponseWriter, r *http.Request) {
		s.generatePoll(w, r, gs)
	})
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(generatePageHTML))
	})
}

func (s *Server) generateStart(w http.ResponseWriter, r *http.Request, gs *genState) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, fmt.Sprintf("parse form: %v", err))
		return
	}
	prompt := strings.TrimSpace(r.FormValue("prompt"))
	site := strings.TrimSpace(r.FormValue("site"))
	identity := strings.TrimSpace(r.FormValue("identity"))
	if identity == "" {
		identity = gs.cfg.DefaultUser
	}
	projectName, _ := normalizeProjectName(r.FormValue("project_name"))
	force := r.FormValue("force") == "1"

	if prompt == "" {
		writeErr(w, 400, "prompt required")
		return
	}
	if !siteRe.MatchString(site) {
		writeErr(w, 400, "site must match ^[a-z0-9-]{1,40}$")
		return
	}
	if !identityRe.MatchString(identity) {
		writeErr(w, 400, "identity must match ^[a-zA-Z0-9_-]{1,20}$")
		return
	}

	// 選用:上傳文件內容併入 prompt
	var docText string
	if file, _, err := r.FormFile("doc"); err == nil {
		defer file.Close()
		buf := make([]byte, 0, 64*1024)
		tmp := make([]byte, 32*1024)
		for {
			n, rerr := file.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if len([]rune(string(buf))) > maxDocRunes || rerr != nil {
				break
			}
		}
		docText = string(buf)
		if rs := []rune(docText); len(rs) > maxDocRunes {
			docText = string(rs[:maxDocRunes])
		}
	}

	fullPrompt := buildPrompt(prompt, docText)
	if len([]rune(fullPrompt)) > maxPromptRunes {
		writeErr(w, 400, fmt.Sprintf("prompt+doc too long (max %d chars)", maxPromptRunes))
		return
	}

	// 站台衝突檢查(跟 uploadSingle 同語意)
	if _, err := os.Stat(filepath.Join(s.Root, site)); err == nil && !force {
		resp := map[string]any{
			"status": "conflict",
			"site":   site,
			"hint":   "use force=1 to overwrite, or pick another site name",
		}
		if existing, _ := meta.Load(s.Root, site); existing != nil {
			resp["existing_uploader"] = existing.Uploader
		}
		writeJSON(w, http.StatusConflict, resp)
		return
	}

	job := &genJob{ID: genID(), Status: "pending", Site: site, CreatedAt: time.Now()}
	gs.mu.Lock()
	gs.jobs[job.ID] = job
	gs.reapLocked()
	gs.mu.Unlock()

	go gs.run(s, job, fullPrompt, site, identity, projectName)

	writeJSON(w, 202, map[string]any{
		"status": "accepted",
		"job":    job.ID,
		"poll":   fmt.Sprintf("/api/generate/%s", job.ID),
	})
}

func (s *Server) generatePoll(w http.ResponseWriter, r *http.Request, gs *genState) {
	id := strings.TrimPrefix(r.URL.Path, "/api/generate/")
	if id == "" {
		writeErr(w, 400, "missing job id")
		return
	}
	gs.mu.Lock()
	job, ok := gs.jobs[id]
	var snapshot genJob
	if ok {
		snapshot = *job // 在鎖內複製一份,避免與 run() 的欄位寫入 data race
	}
	gs.mu.Unlock()
	if !ok {
		writeErr(w, 404, "job not found (may have expired)")
		return
	}
	writeJSON(w, 200, snapshot)
}

func (gs *genState) run(s *Server, job *genJob, prompt, site, identity, projectName string) {
	gs.sem <- struct{}{}
	defer func() { <-gs.sem }()

	gs.setStatus(job.ID, "running", "")

	ctx, cancel := context.WithTimeout(context.Background(), genTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, gs.cfg.ClaudeBin, "-p", prompt,
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "")
	// 生成落在 home,避開 claude session 的 /tmp 沙箱;清掉 CLAUDECODE 讓它走 stateless。
	cmd.Env = append(os.Environ(), "CLAUDECODE=")
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		gs.finishErr(job.ID, "generation timed out")
		return
	}
	if err != nil {
		msg := fmt.Sprintf("claude failed: %v", err)
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			stderr := strings.TrimSpace(string(ee.Stderr))
			if len(stderr) > 500 {
				stderr = stderr[:500]
			}
			msg = fmt.Sprintf("claude failed: %v: %s", err, stderr)
		}
		gs.finishErr(job.ID, msg)
		return
	}

	html, ok := sanitizeGeneratedHTML(string(out))
	if !ok {
		gs.finishErr(job.ID, "no HTML found in model output")
		return
	}

	n, werr := s.writeSiteHTML(site, identity, projectName, "generated", "", []byte(html), DefaultTTL)
	if werr != nil {
		gs.finishErr(job.ID, werr.Error())
		return
	}

	url := fmt.Sprintf("%s/%s/", s.PublicURL, site)
	gs.mu.Lock()
	if j := gs.jobs[job.ID]; j != nil {
		j.Status = "done"
		j.URL = url
		j.SizeBytes = n
	}
	gs.mu.Unlock()
	log.Printf("generate: site=%s uploader=%s size=%d", site, identity, n)
}

func (gs *genState) setStatus(id, status, errMsg string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if j := gs.jobs[id]; j != nil {
		j.Status = status
		if errMsg != "" {
			j.Error = errMsg
		}
	}
}

func (gs *genState) finishErr(id, msg string) {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if j := gs.jobs[id]; j != nil {
		j.Status = "error"
		j.Error = msg
	}
	log.Printf("generate error: job=%s %s", id, msg)
}

// reapLocked 清掉過期 job(呼叫端須持有 mu)。
func (gs *genState) reapLocked() {
	cutoff := time.Now().Add(-genJobTTL)
	for id, j := range gs.jobs {
		if j.CreatedAt.Before(cutoff) {
			delete(gs.jobs, id)
		}
	}
}

func buildPrompt(userReq, docText string) string {
	var b strings.Builder
	b.WriteString("你是網頁生成器。請產生一個『完整、自包含』的 HTML 頁面(含 <!doctype html>),")
	b.WriteString("所有 CSS/JS inline,不引用任何外部 CDN。頁面第一個字元必須是 <,最後是 </html>。")
	b.WriteString("只輸出 HTML 原始碼,不要 markdown 圍欄、不要任何說明文字。\n\n")
	b.WriteString("需求:\n")
	b.WriteString(userReq)
	if strings.TrimSpace(docText) != "" {
		b.WriteString("\n\n參考文件內容(依此製作頁面):\n")
		b.WriteString(docText)
	}
	return b.String()
}

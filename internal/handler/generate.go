package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yanchen184/wez-html/internal/docextract"
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
	maxPromptRunes = 8000 // prompt + 文件內容合計上限
	// refine 要把整頁 HTML 當基底帶回去,上限比 generate 寬。
	maxRefinePromptRunes = 60000
	maxDocRunes          = 30000           // 上傳文件內容上限(會截斷)
	genTimeout           = 3 * time.Minute // 單次生成上限
	maxConcurrentGen     = 3               // 同時生成數
	genJobTTL            = 30 * time.Minute
)

type genJob struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // pending | running | done | error
	Site      string    `json:"site,omitempty"`
	URL       string    `json:"url,omitempty"`
	Error     string    `json:"error,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	Draft     *tplInput `json:"draft,omitempty"` // AI 產模板草稿(templatesAI 的 job 才有)
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
	mux.HandleFunc("/api/refine", func(w http.ResponseWriter, r *http.Request) {
		s.refineStart(w, r, gs)
	})
	s.registerTemplates(mux, gs)
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(generatePageHTML))
	})
	mux.HandleFunc("/edit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(editPageHTML))
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

	// 選用:上傳文件內容併入 prompt。
	// Office 檔(docx/xlsx/pptx)是 zip 二進位,要整份讀完才能解,不能邊讀邊截;
	// 純文字檔照舊。兩邊最後都以 rune 數截斷。
	var docText string
	if file, hdr, err := r.FormFile("doc"); err == nil {
		defer file.Close()
		data, rerr := io.ReadAll(io.LimitReader(file, 8<<20))
		if rerr != nil {
			writeErr(w, 400, fmt.Sprintf("read doc: %v", rerr))
			return
		}
		if docextract.Supported(hdr.Filename) {
			docText, err = docextract.Extract(hdr.Filename, data)
			if err != nil {
				writeErr(w, 400, fmt.Sprintf("cannot parse document: %v", err))
				return
			}
		} else {
			docText = string(data)
		}
		if rs := []rune(docText); len(rs) > maxDocRunes {
			docText = string(rs[:maxDocRunes]) + "\n…(內容過長已截斷)"
		}
	}

	// prompt 與文件各有各的上限:prompt 限 maxPromptRunes,
	// docText 上面已截斷到 maxDocRunes,不再做合計檢查
	// (合計檢查會讓超過 8000 字的文件永遠 400,截斷形同虛設)。
	if len([]rune(prompt)) > maxPromptRunes {
		writeErr(w, 400, fmt.Sprintf("prompt too long (max %d chars)", maxPromptRunes))
		return
	}
	fullPrompt := buildPrompt(prompt, docText)

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

// refineStart — 對「已存在的站台」再下一輪提示詞修改:讀回目前的 index.html 當基底,
// 連同修改指示餵回 claude 重生,覆蓋同一站台(沿用 generate 的 job/輪詢機制)。
//
// 與 generate 的差異:站台必須已存在(404),且只有原上傳者能改(403,同 delete/rename 語意)。
func (s *Server) refineStart(w http.ResponseWriter, r *http.Request, gs *genState) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeErr(w, 400, fmt.Sprintf("parse form: %v", err))
		return
	}
	instruction := strings.TrimSpace(r.FormValue("prompt"))
	site := strings.TrimSpace(r.FormValue("site"))
	identity := strings.TrimSpace(r.FormValue("identity"))
	if identity == "" {
		identity = gs.cfg.DefaultUser
	}

	if instruction == "" {
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

	m, err := meta.Load(s.Root, site)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, 404, "site not found — generate it first")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	if m.Uploader != identity {
		writeErr(w, 403, fmt.Sprintf("only original uploader (%s) can refine", m.Uploader))
		return
	}

	current, err := os.ReadFile(filepath.Join(s.Root, site, "index.html"))
	if err != nil {
		writeErr(w, 500, fmt.Sprintf("read current page: %v", err))
		return
	}

	fullPrompt := buildRefinePrompt(instruction, string(current))
	if len([]rune(fullPrompt)) > maxRefinePromptRunes {
		writeErr(w, 400, "page too large to refine — regenerate instead")
		return
	}

	job := &genJob{ID: genID(), Status: "pending", Site: site, CreatedAt: time.Now()}
	gs.mu.Lock()
	gs.jobs[job.ID] = job
	gs.reapLocked()
	gs.mu.Unlock()

	go gs.run(s, job, fullPrompt, site, identity, m.ProjectName)

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

// execClaude 跑 claude -p 拿輸出;逾時 / 失敗回人話錯誤訊息(errMsg != "" 即失敗)。
// run 與 runTemplateDraft 共用。
func (gs *genState) execClaude(prompt string) (out []byte, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), genTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, gs.cfg.ClaudeBin, "-p", prompt,
		"--permission-mode", "bypassPermissions",
		"--allowedTools", "")
	// 生成落在 home,避開 claude session 的 /tmp 沙箱;清掉 CLAUDECODE 讓它走 stateless。
	cmd.Env = append(os.Environ(), "CLAUDECODE=")
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, "generation timed out"
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
		return nil, msg
	}
	return out, ""
}

func (gs *genState) run(s *Server, job *genJob, prompt, site, identity, projectName string) {
	gs.sem <- struct{}{}
	defer func() { <-gs.sem }()

	gs.setStatus(job.ID, "running", "")

	out, errMsg := gs.execClaude(prompt)
	if errMsg != "" {
		gs.finishErr(job.ID, errMsg)
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

// kvAPIDoc 教生成的頁面用「站台自帶的 KV 資料庫」。
// 每個站台都有 /<site>/api/kv,生成頁用相對路徑就打得到自己的 KV,
// 需要存資料的需求(留言板、報名、投票、待辦…)不必外接後端。
const kvAPIDoc = `本站台自帶一個 key-value 資料庫 API,若需求需要「儲存/讀取資料」(例如留言板、報名表、投票、待辦清單),直接在頁面 JS 用 fetch 相對路徑操作它;純展示頁則不必使用:
- 讀:GET api/kv/<key> → 回該 key 的 JSON;404 = 尚無資料
- 寫:PUT api/kv/<key>,body 必須是合法 JSON(Content-Type: application/json)
- 刪:DELETE api/kv/<key>
- 列出:GET api/kv → {"keys":[{"key":...,"size_bytes":...}],...}
- key 規則 ^[a-zA-Z0-9_-]{1,64}$,不支援巢狀;單值上限 256KB,整站上限 1000 keys / 10MB
- 注意 fetch 一律用相對路徑(如 fetch('api/kv/messages')),頁面網址以 / 結尾,相對路徑會正確落在本站台底下
- 沒有帳號權限機制,寫入是 last-write-wins;清單類資料(如留言)存成單一 key 裡的 JSON 陣列,讀出→append→寫回即可
- 頁面初次載入讀到 404 時視為空資料,正常初始化,不要當錯誤顯示`

// buildRefinePrompt 把「現有頁面 + 這輪修改指示」組成 prompt。
// 重點是要 claude 在既有頁面上改,而不是重畫一頁無關的。
func buildRefinePrompt(instruction, currentHTML string) string {
	var b strings.Builder
	b.WriteString("你是網頁編輯器。下面是一個現有的 HTML 頁面,請依照修改需求改它,")
	b.WriteString("保留沒被要求改動的部分(內容、結構、風格都不要無故重寫)。\n")
	b.WriteString("輸出一個『完整、自包含』的 HTML 頁面(含 <!doctype html>),所有 CSS/JS inline,")
	b.WriteString("不引用任何外部 CDN。頁面第一個字元必須是 <,最後是 </html>。")
	b.WriteString("只輸出 HTML 原始碼,不要 markdown 圍欄、不要任何說明文字。\n\n")
	b.WriteString(kvAPIDoc)
	b.WriteString("\n\n修改需求:\n")
	b.WriteString(instruction)
	b.WriteString("\n\n現有頁面:\n")
	b.WriteString(currentHTML)
	return b.String()
}

func buildPrompt(userReq, docText string) string {
	var b strings.Builder
	b.WriteString("你是網頁生成器。請產生一個『完整、自包含』的 HTML 頁面(含 <!doctype html>),")
	b.WriteString("所有 CSS/JS inline,不引用任何外部 CDN。頁面第一個字元必須是 <,最後是 </html>。")
	b.WriteString("只輸出 HTML 原始碼,不要 markdown 圍欄、不要任何說明文字。\n\n")
	b.WriteString(kvAPIDoc)
	b.WriteString("\n\n需求:\n")
	b.WriteString(userReq)
	if strings.TrimSpace(docText) != "" {
		b.WriteString("\n\n參考文件內容(依此製作頁面):\n")
		b.WriteString(docText)
	}
	return b.String()
}

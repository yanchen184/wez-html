package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// templates.go — /new 頁「使用者自訂模板」的 CRUD + AI 產模板草稿。
//
// 存法走「夠用就好」:root 下一個 .templates.json 收全部模板(內網共用,量小),
// tplMu 保護讀改寫。擁有者語意沿用站台:只有建立者 identity 能改/刪。
// AI 產模板沿用 generate 的 async job 機制(claude -p 超過 http WriteTimeout,不能同步跑)。

const (
	tplFileName   = ".templates.json"
	maxTplName    = 30
	maxTplIcon    = 8
	maxTplDesc    = 60
	maxTplPrompt  = 4000
	maxTplCount   = 200 // 全站模板總數上限,防灌爆
	maxTplAIDescR = 2000
)

// Template 一張使用者自訂模板卡。
type Template struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon"`
	Desc      string    `json:"desc"`
	Prompt    string    `json:"prompt"`
	Identity  string    `json:"identity"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) tplPath() string { return filepath.Join(s.Root, tplFileName) }

// loadTemplatesLocked 讀全部模板;檔案不存在回空 slice。呼叫端須持有 tplMu。
func (s *Server) loadTemplatesLocked() ([]Template, error) {
	b, err := os.ReadFile(s.tplPath())
	if err != nil {
		if os.IsNotExist(err) {
			return []Template{}, nil
		}
		return nil, err
	}
	var out []Template
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("templates file corrupt: %w", err)
	}
	return out, nil
}

// saveTemplatesLocked 原子寫回(temp+rename)。呼叫端須持有 tplMu。
func (s *Server) saveTemplatesLocked(list []Template) error {
	b, err := json.MarshalIndent(list, "", " ")
	if err != nil {
		return err
	}
	tmp := s.tplPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.tplPath())
}

var tplIDRe = regexp.MustCompile(`^t[0-9-]+$`)

func tplID() string {
	genCounter.Lock()
	genCounter.n++
	n := genCounter.n
	genCounter.Unlock()
	return fmt.Sprintf("t%d-%d", time.Now().UnixNano(), n)
}

// tplInput 是 create / update 共用的請求 body。
type tplInput struct {
	Name     string `json:"name"`
	Icon     string `json:"icon,omitempty"`
	Desc     string `json:"desc,omitempty"`
	Prompt   string `json:"prompt"`
	Identity string `json:"identity,omitempty"`
}

// validateTplInput 清洗 + 驗證欄位,回 (清洗後 input, 錯誤訊息)。
func validateTplInput(in tplInput) (tplInput, string) {
	in.Name = strings.TrimSpace(in.Name)
	in.Icon = strings.TrimSpace(in.Icon)
	in.Desc = strings.TrimSpace(in.Desc)
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.Identity = strings.TrimSpace(in.Identity)
	switch {
	case in.Name == "":
		return in, "name required"
	case len([]rune(in.Name)) > maxTplName:
		return in, fmt.Sprintf("name too long (max %d chars)", maxTplName)
	case len([]rune(in.Icon)) > maxTplIcon:
		return in, fmt.Sprintf("icon too long (max %d chars)", maxTplIcon)
	case len([]rune(in.Desc)) > maxTplDesc:
		return in, fmt.Sprintf("desc too long (max %d chars)", maxTplDesc)
	case in.Prompt == "":
		return in, "prompt required"
	case len([]rune(in.Prompt)) > maxTplPrompt:
		return in, fmt.Sprintf("prompt too long (max %d chars)", maxTplPrompt)
	case !identityRe.MatchString(in.Identity):
		return in, "identity must match ^[a-zA-Z0-9_-]{1,20}$"
	}
	if in.Icon == "" {
		in.Icon = "📄"
	}
	return in, ""
}

// registerTemplates 掛模板 CRUD 端點(跟 /new 同壽命,generate enabled 才有)。
func (s *Server) registerTemplates(mux *http.ServeMux, gs *genState) {
	mux.HandleFunc("/api/templates", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.templatesList(w)
		case http.MethodPost:
			s.templatesCreate(w, r)
		default:
			writeErr(w, 405, "GET or POST only")
		}
	})
	mux.HandleFunc("/api/templates/ai", func(w http.ResponseWriter, r *http.Request) {
		s.templatesAI(w, r, gs)
	})
	mux.HandleFunc("/api/templates/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/templates/")
		if id == "ai" { // 保險:有些 client 會打到帶尾斜線的路由
			s.templatesAI(w, r, gs)
			return
		}
		switch r.Method {
		case http.MethodPut:
			s.templatesUpdate(w, r, id)
		case http.MethodDelete:
			s.templatesDelete(w, r, id)
		default:
			writeErr(w, 405, "PUT or DELETE only")
		}
	})
}

func (s *Server) templatesList(w http.ResponseWriter) {
	s.tplMu.Lock()
	list, err := s.loadTemplatesLocked()
	s.tplMu.Unlock()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	writeJSON(w, 200, map[string]any{"templates": list, "count": len(list)})
}

func (s *Server) templatesCreate(w http.ResponseWriter, r *http.Request) {
	var in tplInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, 400, fmt.Sprintf("bad json: %v", err))
		return
	}
	in, msg := validateTplInput(in)
	if msg != "" {
		writeErr(w, 400, msg)
		return
	}

	s.tplMu.Lock()
	defer s.tplMu.Unlock()
	list, err := s.loadTemplatesLocked()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(list) >= maxTplCount {
		writeErr(w, 409, fmt.Sprintf("too many templates (max %d)", maxTplCount))
		return
	}
	now := time.Now()
	t := Template{
		ID: tplID(), Name: in.Name, Icon: in.Icon, Desc: in.Desc,
		Prompt: in.Prompt, Identity: in.Identity, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.saveTemplatesLocked(append(list, t)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, t)
}

func (s *Server) templatesUpdate(w http.ResponseWriter, r *http.Request, id string) {
	if !tplIDRe.MatchString(id) {
		writeErr(w, 400, "bad template id")
		return
	}
	var in tplInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, 400, fmt.Sprintf("bad json: %v", err))
		return
	}
	in, msg := validateTplInput(in)
	if msg != "" {
		writeErr(w, 400, msg)
		return
	}

	s.tplMu.Lock()
	defer s.tplMu.Unlock()
	list, err := s.loadTemplatesLocked()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if list[i].Identity != in.Identity {
			writeErr(w, 403, fmt.Sprintf("only creator (%s) can update", list[i].Identity))
			return
		}
		list[i].Name, list[i].Icon, list[i].Desc, list[i].Prompt = in.Name, in.Icon, in.Desc, in.Prompt
		list[i].UpdatedAt = time.Now()
		if err := s.saveTemplatesLocked(list); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, list[i])
		return
	}
	writeErr(w, 404, "template not found")
}

func (s *Server) templatesDelete(w http.ResponseWriter, r *http.Request, id string) {
	if !tplIDRe.MatchString(id) {
		writeErr(w, 400, "bad template id")
		return
	}
	var in struct {
		Identity string `json:"identity"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeErr(w, 400, fmt.Sprintf("bad json: %v", err))
		return
	}
	in.Identity = strings.TrimSpace(in.Identity)
	if !identityRe.MatchString(in.Identity) {
		writeErr(w, 400, "identity must match ^[a-zA-Z0-9_-]{1,20}$")
		return
	}

	s.tplMu.Lock()
	defer s.tplMu.Unlock()
	list, err := s.loadTemplatesLocked()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if list[i].Identity != in.Identity {
			writeErr(w, 403, fmt.Sprintf("only creator (%s) can delete", list[i].Identity))
			return
		}
		if err := s.saveTemplatesLocked(append(list[:i:i], list[i+1:]...)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"status": "deleted", "id": id})
		return
	}
	writeErr(w, 404, "template not found")
}

// --- AI 產模板草稿 ---

// templatesAI:使用者描述想要的模板 → claude -p 產 {name, icon, desc, prompt} 草稿。
// 走 generate 的 async job(claude 跑 10-60 秒,超過 http WriteTimeout 不能同步回)。
// 草稿只回給前端填表,使用者確認後才走 POST /api/templates 真正儲存。
func (s *Server) templatesAI(w http.ResponseWriter, r *http.Request, gs *genState) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "POST only")
		return
	}
	var in struct {
		Desc string `json:"desc"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		writeErr(w, 400, fmt.Sprintf("bad json: %v", err))
		return
	}
	in.Desc = strings.TrimSpace(in.Desc)
	if in.Desc == "" {
		writeErr(w, 400, "desc required")
		return
	}
	if len([]rune(in.Desc)) > maxTplAIDescR {
		writeErr(w, 400, fmt.Sprintf("desc too long (max %d chars)", maxTplAIDescR))
		return
	}

	job := &genJob{ID: genID(), Status: "pending", CreatedAt: time.Now()}
	gs.mu.Lock()
	gs.jobs[job.ID] = job
	gs.reapLocked()
	gs.mu.Unlock()

	go gs.runTemplateDraft(job, buildTplDraftPrompt(in.Desc))

	writeJSON(w, 202, map[string]any{
		"status": "accepted",
		"job":    job.ID,
		"poll":   fmt.Sprintf("/api/generate/%s", job.ID),
	})
}

func buildTplDraftPrompt(userDesc string) string {
	var b strings.Builder
	b.WriteString("你是網頁模板設計師。使用者要在「AI 生成網站」工具裡新增一張可重複使用的模板卡,")
	b.WriteString("模板的 prompt 之後會被這樣用:『<模板 prompt>\\n\\n我的內容:\\n<使用者貼的資料>』,")
	b.WriteString("所以 prompt 要描述頁面型態、必備區塊、排版風格,結尾不要自己加「我的內容」段。\n")
	b.WriteString("只輸出一個 JSON 物件,不要 markdown 圍欄、不要說明文字,欄位:\n")
	b.WriteString(`{"name":"模板名(10字內)","icon":"單一 emoji","desc":"一句話說明(20字內)","prompt":"給網頁生成器的模板提示詞(繁體中文,150-400字)"}`)
	b.WriteString("\n\n使用者想要的模板:\n")
	b.WriteString(userDesc)
	return b.String()
}

// runTemplateDraft 背景跑 claude 產模板草稿,結果掛在 job.Draft(不落站台)。
func (gs *genState) runTemplateDraft(job *genJob, prompt string) {
	gs.sem <- struct{}{}
	defer func() { <-gs.sem }()

	gs.setStatus(job.ID, "running", "")

	out, errMsg := gs.execClaude(prompt)
	if errMsg != "" {
		gs.finishErr(job.ID, errMsg)
		return
	}
	draft, err := parseTplDraft(string(out))
	if err != nil {
		gs.finishErr(job.ID, err.Error())
		return
	}
	gs.mu.Lock()
	if j := gs.jobs[job.ID]; j != nil {
		j.Status = "done"
		j.Draft = &draft
	}
	gs.mu.Unlock()
}

// parseTplDraft 從 claude 輸出抽出模板草稿 JSON(容忍前後夾雜文字/圍欄)。
func parseTplDraft(raw string) (tplInput, error) {
	if m := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```").FindStringSubmatch(raw); m != nil {
		raw = m[1]
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start == -1 || end <= start {
		return tplInput{}, fmt.Errorf("no JSON object in model output")
	}
	var in tplInput
	if err := json.Unmarshal([]byte(raw[start:end+1]), &in); err != nil {
		return tplInput{}, fmt.Errorf("bad JSON from model: %w", err)
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Name == "" || in.Prompt == "" {
		return tplInput{}, fmt.Errorf("model output missing name/prompt")
	}
	// 超長就截,別讓一次草稿失敗
	if rs := []rune(in.Name); len(rs) > maxTplName {
		in.Name = string(rs[:maxTplName])
	}
	if rs := []rune(in.Desc); len(rs) > maxTplDesc {
		in.Desc = string(rs[:maxTplDesc])
	}
	if rs := []rune(in.Prompt); len(rs) > maxTplPrompt {
		in.Prompt = string(rs[:maxTplPrompt])
	}
	return in, nil
}

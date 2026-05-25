package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yanchen184/wez-html/internal/archive"
)

var (
	siteSanitizeRe = regexp.MustCompile(`[^a-z0-9-]+`)
	identityRe     = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,20}$`)
	siteRe         = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
)

const usage = `wez_upload_html — 把資料夾或單 html 部署到內網靜態站台 (wez-html service)

用法:
  wez_upload_html <path> <identity> [flags]
  wez_upload_html <identity> <path> [flags]       # 順序自動判斷

子命令:
  wez_upload_html --delete <site> <identity>      # 刪除站台
  wez_upload_html --extend <site> <identity> --ttl N   # 純延長 TTL
  wez_upload_html --list                          # 列所有站台

Flags:
  --ttl N         過期天數 (1-180, 預設 30)
  --name xxx      指定 site 名稱(蓋掉自動推導)
  --force         撞名時覆蓋(舊檔殺光,TTL 重置)
  --server URL    Service endpoint (預設 http://localhost:8090)

範例:
  wez_upload_html ./frontend yc
  wez_upload_html ./個人賽.html yc                  # 單檔自動包成資料夾
  wez_upload_html ./個人賽.html yc --name personal-2026
  wez_upload_html ./frontend bob --ttl 90 --name landing-2026
  wez_upload_html ./demo alice --force
  wez_upload_html --delete frontend yc
  wez_upload_html --extend frontend yc --ttl 60

identity 規則: [a-zA-Z0-9_-] 1-20 字
              會記在 .meta.json,日後出事用來找你
              建議寫英文名或工號(yc / bob / alice)
`

func main() {
	var (
		ttl      int
		name     string
		force    bool
		server   string
		doDelete bool
		doExtend bool
		doList   bool
		help     bool
	)
	flag.IntVar(&ttl, "ttl", 30, "TTL days")
	flag.StringVar(&name, "name", "", "override site name")
	flag.BoolVar(&force, "force", false, "overwrite on collision")
	flag.StringVar(&server, "server", "http://localhost:8090", "service endpoint")
	flag.BoolVar(&doDelete, "delete", false, "delete a site")
	flag.BoolVar(&doExtend, "extend", false, "extend TTL only")
	flag.BoolVar(&doList, "list", false, "list all sites")
	flag.BoolVar(&help, "h", false, "help")
	flag.BoolVar(&help, "help", false, "help")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	// 允許 flags 跟 positional args 交錯出現:
	// wez_upload_html ./path yc --ttl 60   ← Go 預設不認,這裡自己拆
	rawArgs := os.Args[1:]
	positionals, flagArgs := splitArgs(rawArgs)
	if err := flag.CommandLine.Parse(flagArgs); err != nil {
		os.Exit(2)
	}

	if help {
		fmt.Print(usage)
		return
	}
	if doList {
		listCmd(server)
		return
	}

	args := positionals

	if doDelete {
		site, identity, err := parseSiteIdentity(args)
		if err != nil {
			die("⛔ %v\n\n用法: wez_upload_html --delete <site> <identity>\n", err)
		}
		deleteCmd(server, site, identity)
		return
	}
	if doExtend {
		site, identity, err := parseSiteIdentity(args)
		if err != nil {
			die("⛔ %v\n\n用法: wez_upload_html --extend <site> <identity> --ttl N\n", err)
		}
		if ttl == 30 {
			fmt.Fprintln(os.Stderr, "提示: 沒帶 --ttl,使用預設 30 天")
		}
		extendCmd(server, site, identity, ttl)
		return
	}

	// upload mode
	path, identity, err := parsePathIdentity(args)
	if err != nil {
		die("%v", err)
	}
	if ttl < 1 || ttl > 180 {
		die("⛔ --ttl 範圍 1-180 (收到 %d)\n", ttl)
	}

	site := name
	if site == "" {
		site = deriveSite(path)
	} else {
		site = sanitizeSite(site)
	}
	if !siteRe.MatchString(site) {
		die("⛔ site name 無效: %q (規則: ^[a-z0-9-]{1,40}$)\n用 --name 指定一個合法名字\n", site)
	}

	uploadCmd(server, path, site, identity, ttl, force)
}

// 解析 path + identity(順序不限)
func parsePathIdentity(args []string) (path, identity string, err error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("⛔ 缺少參數\n\n%s", usage)
	}
	if len(args) == 1 {
		a := args[0]
		if looksLikePath(a) {
			return "", "", fmt.Errorf("⛔ 缺少 identity(誰上傳)\n\n收到的 path: %s\n範例: wez_upload_html %s yc\n\n%s", a, a, usage)
		}
		if identityRe.MatchString(a) {
			return "", "", fmt.Errorf("⛔ 缺少要上傳的目錄\n\n收到的 identity: %s\n範例: wez_upload_html ./frontend %s\n\n%s", a, a, usage)
		}
		return "", "", fmt.Errorf("⛔ 看不懂這個參數: %q\n\n%s", a, usage)
	}
	if len(args) > 2 {
		return "", "", fmt.Errorf("⛔ 太多參數 (只要 path + identity)\n收到: %v\n\n%s", args, usage)
	}

	a, b := args[0], args[1]
	aIsPath := looksLikePath(a)
	bIsPath := looksLikePath(b)
	aIsID := identityRe.MatchString(a)
	bIsID := identityRe.MatchString(b)

	switch {
	case aIsPath && bIsID:
		return a, b, nil
	case bIsPath && aIsID:
		return b, a, nil
	case aIsPath && bIsPath:
		return "", "", fmt.Errorf("⛔ 兩個參數都像路徑: %s / %s\n要先寫 identity 才不會混淆\n範例: wez_upload_html %s yc\n", a, b, a)
	case aIsID && bIsID:
		return "", "", fmt.Errorf("⛔ 兩個參數都像 identity: %s / %s\n看不出哪個是路徑\n範例: wez_upload_html ./frontend %s\n", a, b, a)
	default:
		return "", "", fmt.Errorf("⛔ 無法判斷哪個是 path 哪個是 identity\n收到: %q %q\n\nidentity 規則: [a-zA-Z0-9_-] 1-20 字\npath 範例: ./frontend, ../demo, ~/code/app\n\n%s", a, b, usage)
	}
}

func parseSiteIdentity(args []string) (site, identity string, err error) {
	if len(args) != 2 {
		return "", "", fmt.Errorf("需要剛好 2 個參數 (site, identity)")
	}
	a, b := args[0], args[1]
	aIsSite := siteRe.MatchString(a)
	bIsSite := siteRe.MatchString(b)
	aIsID := identityRe.MatchString(a)
	bIsID := identityRe.MatchString(b)
	switch {
	case aIsSite && bIsID:
		return a, b, nil
	case bIsSite && aIsID:
		return b, a, nil
	default:
		return "", "", fmt.Errorf("無法判斷 %q %q 哪個是 site 哪個是 identity", a, b)
	}
}

func looksLikePath(s string) bool {
	if strings.ContainsAny(s, "/~") {
		return true
	}
	if strings.HasPrefix(s, ".") {
		return true
	}
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return false
}

func deriveSite(srcPath string) string {
	base := filepath.Base(filepath.Clean(srcPath))
	// 單檔:砍副檔名(.html/.htm/.tar.gz/.tgz 等),用主檔名當 site
	lower := strings.ToLower(base)
	for _, suf := range []string{".tar.gz", ".tgz", ".html", ".htm", ".zip"} {
		if strings.HasSuffix(lower, suf) {
			base = base[:len(base)-len(suf)]
			break
		}
	}
	s := sanitizeSite(base)
	if s == "" {
		// 全是非 ascii(例「個人賽」) → sanitize 後空,給時間戳當 fallback
		s = "site-" + time.Now().Format("20060102-150405")
	}
	return s
}

func sanitizeSite(s string) string {
	s = strings.ToLower(s)
	s = siteSanitizeRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func uploadCmd(server, path, site, identity string, ttl int, force bool) {
	info, err := os.Stat(path)
	if err != nil {
		die("⛔ 找不到 %s: %v\n", path, err)
	}
	srcDir := path
	abs, _ := filepath.Abs(path)
	if !info.IsDir() {
		// 單檔模式:.html / .htm 一律包成 <tmp>/index.html;其他副檔名擋下來。
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".html" && ext != ".htm" {
			die("⛔ %s 不是目錄,也不是 .html 檔(允許資料夾或單一 html)\n", path)
		}
		tmp, err := os.MkdirTemp("", "wez-html-single-*")
		if err != nil {
			die("⛔ 建 tmp 失敗: %v\n", err)
		}
		defer os.RemoveAll(tmp)
		dst := filepath.Join(tmp, "index.html")
		in, err := os.Open(path)
		if err != nil {
			die("⛔ 讀 %s 失敗: %v\n", path, err)
		}
		out, err := os.Create(dst)
		if err != nil {
			in.Close()
			die("⛔ 寫 tmp 失敗: %v\n", err)
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			out.Close()
			die("⛔ copy 失敗: %v\n", err)
		}
		in.Close()
		out.Close()
		fmt.Printf("→ 單檔模式:%s → %s/index.html\n", path, tmp)
		srcDir = tmp
	}

	fmt.Printf("→ 打包 %s\n", abs)
	var buf bytes.Buffer
	st, err := archive.Pack(srcDir, &buf)
	if err != nil {
		die("⛔ 打包失敗: %v\n", err)
	}
	fmt.Printf("  %d 檔 / %s\n", st.Files, humanize(st.SizeBytes))

	fmt.Printf("→ 上傳到 %s as site=%s uploader=%s ttl=%dd%s\n",
		server, site, identity, ttl, ifStr(force, " [force]", ""))

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("archive", site+".tar.gz")
	_, _ = io.Copy(fw, &buf)
	_ = mw.WriteField("site", site)
	_ = mw.WriteField("identity", identity)
	_ = mw.WriteField("ttl", fmt.Sprint(ttl))
	_ = mw.WriteField("force", ifStr(force, "1", "0"))
	_ = mw.WriteField("src", abs)
	mw.Close()

	req, _ := http.NewRequest("POST", server+"/api/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := &http.Client{Timeout: 5 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		die("⛔ 連不到 service: %v\n  (檢查 VPN + service 跑著沒: curl %s/api/sites)\n", err, server)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)

	if res.StatusCode == 409 {
		var j map[string]any
		_ = json.Unmarshal(rb, &j)
		fmt.Fprintf(os.Stderr, "⛔ 撞名: %s 已存在 (uploader=%v, 到期 %v)\n", site, j["existing_uploader"], j["existing_expires_at"])
		fmt.Fprintln(os.Stderr, "選一個:")
		fmt.Fprintf(os.Stderr, "  wez_upload_html %s %s --force            # 覆蓋\n", path, identity)
		fmt.Fprintf(os.Stderr, "  wez_upload_html %s %s --name <別名>      # 換名\n", path, identity)
		os.Exit(1)
	}
	if res.StatusCode != 200 {
		die("⛔ HTTP %d: %s\n", res.StatusCode, string(rb))
	}
	var j map[string]any
	_ = json.Unmarshal(rb, &j)
	fmt.Println()
	fmt.Println("✅ 上傳完成")
	fmt.Printf("   URL:        %v\n", j["url"])
	fmt.Printf("   Uploader:   %v\n", j["uploader"])
	fmt.Printf("   到期:        %v\n", j["expires_at"])
	fmt.Printf("   Size:       %d B / %v files\n", int64(toFloat(j["size_bytes"])), j["files"])
	fmt.Println()
	fmt.Println("   再上傳:   wez_upload_html", path, identity)
	fmt.Println("   延長:     wez_upload_html --extend", site, identity, "--ttl 60")
	fmt.Println("   下架:     wez_upload_html --delete", site, identity)
}

func deleteCmd(server, site, identity string) {
	body, _ := json.Marshal(map[string]string{"identity": identity})
	req, _ := http.NewRequest("DELETE", server+"/api/site/"+site, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		die("⛔ %v\n", err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		die("⛔ HTTP %d: %s\n", res.StatusCode, string(rb))
	}
	fmt.Printf("✅ 已刪除 %s\n", site)
}

func extendCmd(server, site, identity string, ttl int) {
	body, _ := json.Marshal(map[string]any{"identity": identity, "ttl": ttl})
	req, _ := http.NewRequest("POST", server+"/api/site/"+site+"/extend", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		die("⛔ %v\n", err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		die("⛔ HTTP %d: %s\n", res.StatusCode, string(rb))
	}
	var j map[string]any
	_ = json.Unmarshal(rb, &j)
	fmt.Printf("✅ 已延長 %s — 新到期: %v\n", site, j["expires_at"])
}

func listCmd(server string) {
	res, err := http.Get(server + "/api/sites")
	if err != nil {
		die("⛔ %v\n", err)
	}
	defer res.Body.Close()
	rb, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		die("⛔ HTTP %d: %s\n", res.StatusCode, string(rb))
	}
	var j struct {
		Sites []struct {
			Name      string `json:"name"`
			Uploader  string `json:"uploader"`
			DaysLeft  int    `json:"days_left"`
			SizeHuman string `json:"size_human"`
			URL       string `json:"url"`
		} `json:"sites"`
	}
	_ = json.Unmarshal(rb, &j)
	if len(j.Sites) == 0 {
		fmt.Println("(沒有站台)")
		return
	}
	fmt.Printf("%-30s %-12s %6s %8s  %s\n", "SITE", "UPLOADER", "DAYS", "SIZE", "URL")
	for _, s := range j.Sites {
		fmt.Printf("%-30s %-12s %5dd %8s  %s%s\n", s.Name, s.Uploader, s.DaysLeft, s.SizeHuman, server, s.URL)
	}
}

func humanize(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%dB", n)
	}
	u := []string{"K", "M", "G"}
	x := float64(n)
	i := -1
	for x >= k && i < len(u)-1 {
		x /= k
		i++
	}
	return fmt.Sprintf("%.1f%s", x, u[i])
}

func ifStr(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
	os.Exit(1)
}

// splitArgs: 把 ["./path", "yc", "--ttl", "60", "--force", "--name", "x"] 拆成
//   positionals=["./path", "yc"]  flagArgs=["--ttl", "60", "--force", "--name", "x"]
// 需值的 flag 後面那個 token 一律當值帶走,布林 flag 不帶值。
func splitArgs(args []string) (positionals, flagArgs []string) {
	needsValue := map[string]bool{
		"ttl": true, "name": true, "server": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			return
		}
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			// 處理 --key=value 形式:已含 =,不需吃下一個
			if strings.Contains(a, "=") {
				continue
			}
			// 刨掉前綴
			key := strings.TrimLeft(a, "-")
			if needsValue[key] && i+1 < len(args) {
				flagArgs = append(flagArgs, args[i+1])
				i++
			}
			continue
		}
		positionals = append(positionals, a)
	}
	return
}

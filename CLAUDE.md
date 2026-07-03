# wez-html / html.yanchen.app

這個 repo 有兩個東西:
1. **wez-html**(根目錄,Go):原版單檔 HTML 託管 server + CLI,公司內網用。
2. **html.yanchen.app**(`cloudflare/`,Pages Functions + Workers KV):wez-html 的 Cloudflare 移植,Bob 的**個人迷你託管站 + 所有專案的公開狀態看板**,線上 `https://html.yanchen.app`。

## html.yanchen.app 就是「狀態看板」本身

全域 CLAUDE.md 規定:新專案要把「功能 + 驗收標準 + 進度」做成單一 HTML 推到 `html.yanchen.app/<slug>/`(用 `/html-deploy`),之後有更新回來再推。

**這個 repo 是那個看板的後端平台。** 所以:
- 平台本身(`cloudflare/` 的 functions / 皮 / KV schema)有實質更新 → 用 `/cf-deploy html`(或 `cd cloudflare && npx wrangler pages deploy ...`)重新部署平台,並 round-trip 驗 `https://html.yanchen.app/api/sites` 真的回 200(不是看 build 綠燈)。
- 部署細節、KV id、ADMIN_TOKEN 位置、踩過的坑:見專案 memory `~/.claude/projects/-Users-yanchen-workspace-wez-html/memory/project_html_yanchen_app_deploy.md`;token 在 `~/.claude/memory/credentials.md`。

## 部署用哪支 command
- **改平台本身**(functions/皮/KV)→ `/cf-deploy html` 或 `wrangler pages deploy`(見上)。
- **掛/更新一個 HTML 站**(含未來各專案的狀態頁)→ `/html-deploy <html路徑> [站名]`。

## 紀律
- ADMIN_TOKEN 等機密**絕不進 repo**(`.dev.vars` 已 gitignore;線上走 Cloudflare secret)。
- `cloudflare/wrangler.toml` 的 KV id 非機密,可 commit。
- 改平台跨層(functions + 皮 + KV)→ 交付前自己 round-trip 驗線上,再回報。

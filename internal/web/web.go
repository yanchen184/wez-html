package web

import "embed"

// index.html 是「單一源皮」:Go 端 embed 直接吐、Cloudflare Pages 端當 public/index.html。
// 站台資料一律前端 fetch /api/sites 渲染,兩端共用同一份皮,改一次兩邊一致。
//
//go:embed index.html favicon.svg
var FS embed.FS

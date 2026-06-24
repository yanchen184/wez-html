package web

import "embed"

//go:embed index.html.tmpl favicon.svg
var FS embed.FS

package main

import (
	"context"
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yanchen184/wez-html/internal/handler"
	"github.com/yanchen184/wez-html/internal/web"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8090", "listen address")
	root := flag.String("root", "/var/lib/wez-html", "site root dir")
	publicURL := flag.String("public-url", "http://localhost:8090", "public URL (for upload response)")
	reapInterval := flag.Duration("reap-interval", 6*time.Hour, "expired site sweep interval")
	flag.Parse()

	if err := os.MkdirAll(*root, 0o755); err != nil {
		log.Fatalf("mkdir root: %v", err)
	}

	tmplBytes, err := web.FS.ReadFile("index.html.tmpl")
	if err != nil {
		log.Fatalf("embed index tmpl: %v", err)
	}
	tmpl, err := template.New("index").Parse(string(tmplBytes))
	if err != nil {
		log.Fatalf("parse index tmpl: %v", err)
	}
	faviconBytes, err := web.FS.ReadFile("favicon.svg")
	if err != nil {
		log.Fatalf("embed favicon: %v", err)
	}

	srv := handler.New(*root, *publicURL, tmpl, string(faviconBytes))
	mux := http.NewServeMux()
	srv.Routes(mux)

	// 自動下架已停用:站台上傳後常駐,不再因 TTL 被自動清掉。
	// --reap-interval flag 仍接收以維持既有 service / 呼叫相容,但不再啟動 sweep。
	_ = reapInterval

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Minute, // 大檔上傳
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("wez-html listening on %s, root=%s", *listen, *root)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutdown")
	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	_ = httpSrv.Shutdown(shutdownCtx)
}

.PHONY: build build-linux build-windows build-cli-all build-cli build-server clean run-local deploy deploy-cf deploy-all deploy-backup install-cli

VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BIN_DIR := bin

# Deploy targets — 改成你自己的:
#   WEZ_HOST         = SSH host (~/.ssh/config 的 alias 或 user@ip)
#   WEZ_USER         = 跑 systemd unit 的 OS 帳號
#   WEZ_PUBLIC_HOST  = 同事瀏覽器打的對外 host(IP / domain)— 別用 SSH alias
#   GOARCH           = 目標機器架構 (arm64 / amd64)
WEZ_HOST        ?= my-server
WEZ_USER        ?= deploy
WEZ_PUBLIC_HOST ?= $(WEZ_HOST)
GOARCH          ?= arm64

build: build-cli build-server

build-cli:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/wez_upload_html ./cmd/cli

build-server:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/wez-html-server ./cmd/server

# Cross-compile for target host (預設 arm64,改 GOARCH=amd64 走 x86)
build-linux:
	@mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=$(GOARCH) go build -o $(BIN_DIR)/wez-html-server-linux ./cmd/server
	GOOS=linux GOARCH=$(GOARCH) go build -o $(BIN_DIR)/wez_upload_html-linux ./cmd/cli

# Cross-compile the CLI for Windows (amd64 + arm64)。CLI 純 Go、無平台相依路徑,
# 給 Windows 同事推資料夾用。產物副檔名一定要 .exe。
build-windows:
	@mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/wez_upload_html-windows-amd64.exe ./cmd/cli
	GOOS=windows GOARCH=arm64 go build -o $(BIN_DIR)/wez_upload_html-windows-arm64.exe ./cmd/cli

# 一次出齊三平台的 CLI(mac 原生 + linux + windows),發 binary 給同事用
build-cli-all: build-cli
	@mkdir -p $(BIN_DIR)
	GOOS=linux   GOARCH=amd64 go build -o $(BIN_DIR)/wez_upload_html-linux-amd64 ./cmd/cli
	GOOS=linux   GOARCH=arm64 go build -o $(BIN_DIR)/wez_upload_html-linux-arm64 ./cmd/cli
	GOOS=windows GOARCH=amd64 go build -o $(BIN_DIR)/wez_upload_html-windows-amd64.exe ./cmd/cli
	GOOS=windows GOARCH=arm64 go build -o $(BIN_DIR)/wez_upload_html-windows-arm64.exe ./cmd/cli
	@echo "✓ CLI built for mac(native) + linux(amd64/arm64) + windows(amd64/arm64) in $(BIN_DIR)/"

# Run server locally for dev (uses ./.wez-html-data as root)
run-local: build-server
	mkdir -p .wez-html-data
	$(BIN_DIR)/wez-html-server --listen 127.0.0.1:8090 --root ./.wez-html-data --public-url http://127.0.0.1:8090 --reap-interval 1m

# Install CLI locally to /usr/local/bin (needs sudo)
install-cli: build-cli
	sudo install -m 0755 $(BIN_DIR)/wez_upload_html /usr/local/bin/wez_upload_html
	@echo "✓ installed: $$(which wez_upload_html)"

# Deploy server to remote host
#   make deploy WEZ_HOST=myhost WEZ_USER=myuser GOARCH=arm64
# scripts/wez-html.service 的 CHANGE_ME 會被 sed 換成 $(WEZ_USER) / $(WEZ_HOST)
deploy: build-linux
	@echo "→ deploy to $(WEZ_USER)@$(WEZ_HOST) (arch=$(GOARCH))"
	@mkdir -p $(BIN_DIR)
	sed -e 's|User=CHANGE_ME|User=$(WEZ_USER)|' \
	    -e 's|/home/CHANGE_ME|/home/$(WEZ_USER)|' \
	    -e 's|http://CHANGE_ME:8090|http://$(WEZ_PUBLIC_HOST):8090|' \
	    scripts/wez-html.service > $(BIN_DIR)/wez-html.service.rendered
	scp $(BIN_DIR)/wez-html-server-linux $(WEZ_HOST):/tmp/wez-html-server
	scp $(BIN_DIR)/wez-html.service.rendered $(WEZ_HOST):/tmp/wez-html.service
	ssh $(WEZ_HOST) 'sudo systemctl stop wez-html 2>/dev/null; \
	         sudo install -m 0755 /tmp/wez-html-server /usr/local/bin/wez-html-server && \
	         sudo mv /tmp/wez-html.service /etc/systemd/system/wez-html.service && \
	         sudo mkdir -p /var/lib/wez-html && \
	         sudo chown $(WEZ_USER):$(WEZ_USER) /var/lib/wez-html && \
	         sudo systemctl daemon-reload && \
	         sudo systemctl enable --now wez-html && \
	         sudo systemctl restart wez-html && \
	         sleep 2 && \
	         curl -sf http://127.0.0.1:8090/api/sites > /dev/null && echo "✓ wez-html up"'

# Deploy Cloudflare Pages (public/ 靜態皮 + functions/ 一起推)
# wrangler.toml 已設 pages_build_output_dir=public、functions/ 同層自動抓。
# 需先登入(npx wrangler login)或設 CLOUDFLARE_API_TOKEN / CLOUDFLARE_ACCOUNT_ID。
# ADMIN_TOKEN 走 secret,不在這推(npx wrangler pages secret put ADMIN_TOKEN)。
deploy-cf:
	@echo "→ deploy Cloudflare Pages: html-yanchen-app (production branch=cloudflare-pages)"
	@# 這專案的 production branch 是 cloudflare-pages(不是 main);
	@# 用它才會進 production(html.yanchen.app + ADMIN_TOKEN secret 所在),否則落進 Preview。
	cd cloudflare && npx wrangler pages deploy --branch cloudflare-pages --commit-dirty=true
	@echo "✓ 驗一下:curl -s https://html.yanchen.app/api/sites | head -c 200"

# 一鍵雙推:wez 內網(需在內網跑) + Cloudflare。
# 兩端 embed 同一份 internal/web/index.html 皮 → 功能一致。
#   make deploy-all WEZ_HOST=wez WEZ_USER=ycchen
deploy-all: deploy deploy-cf
	@echo "✓ 雙推完成:wez(http://$(WEZ_PUBLIC_HOST):8090) + https://html.yanchen.app"

# 裝 wez 資料備份(每日 tar 快照 /var/lib/wez-html → /var/backups/wez-html,留最近 14 份)
#   make deploy-backup WEZ_HOST=wez
# 裝完立刻手動跑一次驗:ssh <host> 'sudo systemctl start wez-html-backup && ls -lh /var/backups/wez-html'
deploy-backup:
	@echo "→ 裝資料備份 timer 到 $(WEZ_HOST)"
	scp scripts/wez-html-backup.sh $(WEZ_HOST):/tmp/wez-html-backup.sh
	scp scripts/wez-html-backup.service $(WEZ_HOST):/tmp/wez-html-backup.service
	scp scripts/wez-html-backup.timer $(WEZ_HOST):/tmp/wez-html-backup.timer
	ssh $(WEZ_HOST) 'sudo install -m 0755 /tmp/wez-html-backup.sh /usr/local/bin/wez-html-backup.sh && \
	         sudo mv /tmp/wez-html-backup.service /etc/systemd/system/wez-html-backup.service && \
	         sudo mv /tmp/wez-html-backup.timer /etc/systemd/system/wez-html-backup.timer && \
	         sudo systemctl daemon-reload && \
	         sudo systemctl enable --now wez-html-backup.timer && \
	         echo "→ 跑一次備份驗證" && \
	         sudo systemctl start wez-html-backup.service && \
	         sleep 2 && \
	         sudo ls -lh /var/backups/wez-html/ && \
	         echo "✓ 備份已裝,timer 狀態:" && \
	         sudo systemctl status wez-html-backup.timer --no-pager | head -4'

clean:
	rm -rf $(BIN_DIR) .wez-html-data

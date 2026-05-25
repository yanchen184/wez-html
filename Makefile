.PHONY: build build-linux build-cli build-server clean run-local deploy install-cli

VERSION := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BIN_DIR := bin

# Deploy targets — 改成你自己的:
#   WEZ_HOST  = SSH host (~/.ssh/config 的 alias 或 user@ip)
#   WEZ_USER  = 跑 systemd unit 的 OS 帳號
#   GOARCH    = 目標機器架構 (arm64 / amd64)
WEZ_HOST ?= my-server
WEZ_USER ?= deploy
GOARCH   ?= arm64

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

# Run server locally for dev (uses ./.wez-html-data as root)
run-local: build-server
	mkdir -p .wez-html-data
	$(BIN_DIR)/wez-html-server --listen 127.0.0.1:8090 --root ./.wez-html-data --public-url http://127.0.0.1:8090 --reap-interval 1m

# Install CLI locally to /usr/local/bin (needs sudo)
install-cli: build-cli
	sudo install -m 0755 $(BIN_DIR)/wez_upload_html /usr/local/bin/wez_upload_html
	@echo "✓ installed: $$(which wez_upload_html)"

# Deploy server to remote host (需先改好 scripts/wez-html.service 並有 SSH + sudo)
#   make deploy WEZ_HOST=myhost WEZ_USER=myuser GOARCH=arm64
deploy: build-linux
	@echo "→ deploy to $(WEZ_USER)@$(WEZ_HOST) (arch=$(GOARCH))"
	scp $(BIN_DIR)/wez-html-server-linux $(WEZ_HOST):/tmp/wez-html-server
	scp scripts/wez-html.service $(WEZ_HOST):/tmp/wez-html.service
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

clean:
	rm -rf $(BIN_DIR) .wez-html-data

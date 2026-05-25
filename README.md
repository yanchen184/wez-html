# wez-html

> 給小團隊內部用的 demo 站台 service。一行 command 把任何前端 / 單一 html 部署上去,**附過期、附 uploader 追溯、附刪除/延長介面**。

```bash
$ wez_upload_html ./frontend yc
✅ http://your-server:8090/frontend/  · 到期 2026-06-24
```

## 為什麼有這個東西

過去 demo 一個前端要做這些事:

1. `rsync` 推檔到內網某台機器
2. `ssh` 進去找個沒被佔的 port
3. `python -m http.server` 或起個 nginx
4. 貼 URL 給同事
5. 兩個月後忘了清,垃圾留在機器上

`wez-html` 把這些事壓成一句 CLI。再加三件原本沒有的:

- **TTL 強制** — 預設 30 天、上限 180 天,過期 service 自動 `rm -rf`,不再有「忘記清」的垃圾
- **Uploader 追溯** — 每個站台記 uploader identity,只有同一個 identity 能刪/延長
- **Web 介面** — 拖檔上傳 + 站台列表 + 一鍵刪除/延長,不會 CLI 也能用

## 適合 / 不適合

| 適合 | 不適合 |
|---|---|
| 內網 demo / poc / 個人賽作品分享 | Production hosting |
| 短期(< 半年)的 landing / 投影片 | 需要長期穩定 URL |
| 純靜態檔(html/css/js/圖) | 要接 DB / 後端 API(等 v2) |
| 內網信任環境(VPN / 辦公室 LAN) | 公網開放(沒驗證、純 identity 追溯) |

## 兩種上傳方式

### 1. CLI(本機)

```bash
# 資料夾
wez_upload_html ./frontend yc

# 單一 html(自動包成 <site>/index.html)
wez_upload_html ./個人賽.html yc --name personal-contest

# 帶 TTL / 覆蓋撞名 / 自訂名稱
wez_upload_html ./demo bob --ttl 90 --name landing-2026
wez_upload_html ./demo alice --force

# 管理
wez_upload_html --list
wez_upload_html --delete frontend yc
wez_upload_html --extend frontend yc --ttl 60
```

`--server` flag 蓋掉預設 endpoint(預設 `http://localhost:8090`)。

### 2. Web UI

打開 `http://your-server:8090/`,拖一個 `.html` 或 `.tar.gz` 進拖檔區,填 identity + TTL,送出。

## Build & 本機跑

```bash
make build       # build CLI + server
make run-local   # 本機 server: http://127.0.0.1:8090
```

另開 terminal:

```bash
mkdir -p /tmp/demo && echo '<h1>hi</h1>' > /tmp/demo/index.html
./bin/wez_upload_html /tmp/demo me --server http://127.0.0.1:8090
```

## 部署到 server

```bash
# 1. 編輯 scripts/wez-html.service,把 User / WorkingDirectory / --public-url 改成你的環境
# 2. 編輯 Makefile 的 WEZ_HOST / WEZ_USER,或用環境變數覆蓋
make deploy WEZ_HOST=myserver WEZ_USER=ubuntu GOARCH=arm64
```

要求:
- 該 host 有 SSH(`~/.ssh/config` 的 alias 或 `user@ip` 都行)
- 該 user 在 host 上有 `sudo` 權限(裝 binary + systemd unit)
- 目標機器架構對應 `GOARCH`(預設 `arm64`,x86 機改 `amd64`)

## 架構

- **Go single binary**(`wez-html-server` + `wez_upload_html` CLI),systemd 跑著
- 純檔案儲存 `/var/lib/wez-html/<site>/`,每站附一個 `.meta.json` 記 uploader / expires_at
- 過期清理在 server 程序內,內建 6h ticker 掃過期 site → `os.RemoveAll`
- SPA fallback:非 asset 路徑回 `index.html`,react-router 之類前端不會 404
- 上傳走 multipart;CLI 在本機打 tar.gz,Web UI 走 `/api/upload-single` 給 server 端建 wrapper

```
.
├── cmd/
│   ├── cli/         # wez_upload_html
│   └── server/      # wez-html-server
├── internal/
│   ├── archive/     # tar.gz pack/unpack with size/path limits
│   ├── handler/     # HTTP routes
│   ├── meta/        # .meta.json read/write
│   ├── reaper/      # TTL sweeper
│   └── web/         # 內嵌 index.html template (embed.FS)
└── scripts/
    └── wez-html.service
```

## 限制

- **單檔 ≤ 50MB,單站 ≤ 500MB,共 ≤ 10000 檔**(在 `internal/archive/archive.go` 改)
- **TTL 1–180 天**(在 `internal/handler/handler.go` 的 `MinTTL` / `MaxTTL` 改)
- **identity 純追溯,不驗證**(內網信任模型)— 別人知道你的 identity 就能刪你的站,所以 identity 別用太通用的值
- **不支援 HTTPS**(對外端用 nginx / Caddy 反向代理)

## v2 規劃

- SQLite-as-a-service(Datasette 反向代理)讓前端能接 DB
- HTTPS / Basic Auth(視部署環境)
- Web UI 上的 batch upload + rename

## License

MIT — 隨便拿去用,別告我就好。

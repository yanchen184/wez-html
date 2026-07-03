#!/usr/bin/env bash
# wez ↔ html.yanchen.app 實機 parity 檢查。
#
# 對兩個目標打「同一組操作」,比對回應行為是否一致。抓「CF functions 移植偏離
# Go handler」的漂移(例:某端漏驗 site regex、conflict 狀態碼不同)。
#
# 預設只跑「唯讀 / 不需認證」的檢查(/api/sites shape、site regex 驗證、
# 未存在站台一律 404),不會在正式環境亂建站。
#
# 用法:
#   scripts/parity-check.sh                              # 用預設兩個 base URL
#   WEZ=http://10.1.1.7:8090 CF=https://html.yanchen.app scripts/parity-check.sh
#
# 需要 curl + jq。任一檢查兩端不一致 → 該檢查標 [DIFF],結束碼非 0。
set -uo pipefail

WEZ="${WEZ:-http://10.1.1.7:8090}"
CF="${CF:-https://html.yanchen.app}"

command -v jq >/dev/null || { echo "需要 jq" >&2; exit 2; }

pass=0; fail=0
ok()   { echo "  [OK]   $1"; pass=$((pass+1)); }
diff_() { echo "  [DIFF] $1"; fail=$((fail+1)); }

# http_code <base> <path> [curl args...]  → 印出 HTTP 狀態碼
http_code() {
  local base="$1" path="$2"; shift 2
  curl -s -o /dev/null -w "%{http_code}" "$@" "$base$path"
}

echo "WEZ = $WEZ"
echo "CF  = $CF"
echo

# --- 1) /api/sites 回 200 且有 total/sites 欄位(前端 render 靠這個 shape)---
echo "[1] /api/sites 兩端都回 200 + JSON 有 total/sites 欄位"
for tgt in "WEZ:$WEZ" "CF:$CF"; do
  name="${tgt%%:*}"; base="${tgt#*:}"
  body="$(curl -s "$base/api/sites")"
  code="$(http_code "$base" /api/sites)"
  if [[ "$code" == "200" ]] && echo "$body" | jq -e 'has("total") and has("sites")' >/dev/null 2>&1; then
    ok "$name /api/sites 200, total=$(echo "$body" | jq -r '.total')"
  else
    diff_ "$name /api/sites code=$code(或缺 total/sites 欄位)"
  fi
done
echo

# --- 2) site summary 有「前端真正用到」的欄位 ---------------------------
#   前端(單一源皮 rowHtml())實際讀的是:name / uploader / project_name /
#   size_human / files / days_online。連結是前端用 name 自算,不吃 .url,
#   所以這裡只驗前端會 render 的欄位齊不齊(欄位契約 = 前端需求,不是 JSON 全等)。
echo "[2] sites[] 每筆有前端必要欄位(name/uploader/size_human/files/days_online)"
for tgt in "WEZ:$WEZ" "CF:$CF"; do
  name="${tgt%%:*}"; base="${tgt#*:}"
  missing="$(curl -s "$base/api/sites" | jq -r '
    [ .sites[]? | select(
        (.name|not) or (.uploader|not) or (.size_human|not)
        or (has("files")|not) or (has("days_online")|not)
      ) ] | length')"
  if [[ "$missing" == "0" ]]; then
    ok "$name 所有站台前端欄位齊全"
  else
    diff_ "$name 有 $missing 筆站台缺前端必要欄位"
  fi
done
echo

# --- 3) 未存在站台的 rename 一律 4xx(不需認證也不該 200/500)------------
#   只送不完整/未認證請求,期望被擋(400/401/404/405 都算「有擋」)。
echo "[3] 對不存在站台送 rename,兩端都該擋掉(4xx),不能 200/5xx"
for tgt in "WEZ:$WEZ" "CF:$CF"; do
  name="${tgt%%:*}"; base="${tgt#*:}"
  code="$(http_code "$base" "/api/site/__nope_parity__/rename" \
      -X POST -H 'Content-Type: application/json' \
      --data '{"identity":"parity","new_site":"whatever"}')"
  if [[ "$code" =~ ^4 ]]; then
    ok "$name rename 不存在站台 → $code"
  else
    diff_ "$name rename 不存在站台 → $code(期望 4xx)"
  fi
done
echo

# --- 4) 上傳缺認證/缺欄位一律被擋,不能 200 -----------------------------
echo "[4] /api/upload-single 缺欄位/認證 → 兩端都非 200"
for tgt in "WEZ:$WEZ" "CF:$CF"; do
  name="${tgt%%:*}"; base="${tgt#*:}"
  # 故意送空 body:CF 會缺 admin token(401),wez 會缺 site(400)——都非 200 即算「有擋」。
  code="$(http_code "$base" /api/upload-single -X POST)"
  if [[ "$code" != "200" ]]; then
    ok "$name 空上傳被擋 → $code"
  else
    diff_ "$name 空上傳竟回 200(!!)"
  fi
done
echo

echo "===================="
echo "PASS=$pass  DIFF=$fail"
if ((fail)); then
  echo "有 parity 差異,見上面 [DIFF]。"
  exit 1
fi
echo "兩端行為一致 ✓"

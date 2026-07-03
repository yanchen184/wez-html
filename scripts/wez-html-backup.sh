#!/usr/bin/env bash
# wez-html 資料備份:把 --root(站台資料)打成 tar.gz 快照,保留最近 N 份。
#
# 預設同機備份(防誤刪 / 誤覆蓋 / force 覆蓋洗掉舊站)。整機掛掉需異機備份:
# 見檔末「異機備份」註解,把 rsync 那段打開、填目的地即可。
#
# 由 systemd timer(wez-html-backup.timer)每天叫一次;也可手動跑。
set -euo pipefail

SRC="${WEZ_HTML_ROOT:-/var/lib/wez-html}"
DEST="${WEZ_HTML_BACKUP_DIR:-/var/backups/wez-html}"
KEEP="${WEZ_HTML_BACKUP_KEEP:-14}"   # 保留最近幾份

if [[ ! -d "$SRC" ]]; then
  echo "[wez-html-backup] 來源不存在:$SRC" >&2
  exit 1
fi

mkdir -p "$DEST"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$DEST/wez-html-$STAMP.tar.gz"

# -C 讓 tar 內路徑相對於 SRC 的父目錄,還原時乾淨。
tar -czf "$OUT" -C "$(dirname "$SRC")" "$(basename "$SRC")"
echo "[wez-html-backup] 已備份:$OUT ($(du -h "$OUT" | cut -f1))"

# 輪替:只留最近 $KEEP 份,多的刪掉(按檔名時間序)。
mapfile -t OLD < <(ls -1t "$DEST"/wez-html-*.tar.gz 2>/dev/null | tail -n +"$((KEEP + 1))")
if ((${#OLD[@]})); then
  printf '%s\n' "${OLD[@]}" | xargs -r rm -f
  echo "[wez-html-backup] 清掉 ${#OLD[@]} 份舊備份(保留最近 $KEEP)"
fi

# --- 異機備份(選用,強烈建議之後補)---------------------------------
# 同機備份救不了整機故障。要異機,填好目的地把下面兩行打開:
#   RSYNC_TARGET="user@backup-host:/path/wez-html-backups/"
#   rsync -az "$OUT" "$RSYNC_TARGET"
# ------------------------------------------------------------------

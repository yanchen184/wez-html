// GET /api/sites   列出所有站台（給首頁皮的表格用）
// 公開可讀（列表本身不含敏感資料）；上傳/刪才需 token。

import { META_PREFIX, json } from "../_shared.js";

export async function onRequest(context) {
  const { env } = context;

  // 列出所有 meta:<site> key
  const list = await env.SITES.list({ prefix: META_PREFIX });
  const sites = [];
  let totalSize = 0;

  for (const k of list.keys) {
    const raw = await env.SITES.get(k.name);
    if (!raw) continue;
    const m = JSON.parse(raw);
    totalSize += m.size_bytes || 0;
    sites.push({
      project_name: m.project_name || "",
      name: m.site,
      uploader: m.uploader,
      uploaded_at: m.uploaded_at,
      size_bytes: m.size_bytes || 0,
      size_human: humanSize(m.size_bytes || 0),
      src_path: m.src_path || "",
      files: Array.isArray(m.files) ? m.files.length : 1,
      days_online: daysSince(m.uploaded_at),
    });
  }

  // 最新上傳排前面
  sites.sort((a, b) => (b.uploaded_at || "").localeCompare(a.uploaded_at || ""));

  return json({
    total: sites.length,
    total_size: humanSize(totalSize),
    sites,
  });
}

function humanSize(n) {
  if (n < 1024) return n + "B";
  const u = ["K", "M", "G"];
  let i = -1;
  do {
    n /= 1024;
    i++;
  } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(1) + u[i];
}

function daysSince(iso) {
  if (!iso) return 0;
  const ms = Date.now() - new Date(iso).getTime();
  return Math.max(0, Math.floor(ms / 86400000));
}

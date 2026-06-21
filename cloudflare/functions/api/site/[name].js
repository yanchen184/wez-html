// DELETE /api/site/<name>   刪整個站台（含其所有檔案 + 站台級 KV data）
// 公網強化：需 admin token。identity 仍比對 meta.uploader 當第二道（追溯）。

import {
  SITE_RE,
  META_PREFIX,
  FILE_PREFIX,
  DATA_PREFIX,
  json,
  err,
  requireAdmin,
} from "../../_shared.js";

export async function onRequest(context) {
  const { params, request, env } = context;
  const site = params.name;

  if (request.method !== "DELETE") return err("DELETE only", 405);
  if (!SITE_RE.test(site)) return err("bad site name", 400);

  const authErr = requireAdmin(request, env);
  if (authErr) return err(authErr, authErr === "unauthorized" ? 401 : 500);

  const metaRaw = await env.SITES.get(META_PREFIX + site);
  if (!metaRaw) return err("not found", 404);

  // identity 比對（沿用 wez-html 追溯模型；token 已是主要關卡，這層是額外保險）
  let body = {};
  try {
    body = await request.json();
  } catch {
    body = {};
  }
  const meta = JSON.parse(metaRaw);
  const identity = (body.identity || "").trim();
  if (identity && identity !== meta.uploader)
    return err(`identity mismatch (uploaded by ${meta.uploader})`, 403);

  // 刪 meta + 所有 file:<site>/* + 所有 data:<site>/*
  await env.SITES.delete(META_PREFIX + site);
  await deletePrefix(env.SITES, FILE_PREFIX + site + "/");
  await deletePrefix(env.SITES, DATA_PREFIX + site + "/");

  return json({ status: "deleted", site });
}

async function deletePrefix(kv, prefix) {
  let cursor;
  do {
    const res = await kv.list({ prefix, cursor });
    for (const k of res.keys) await kv.delete(k.name);
    cursor = res.list_complete ? null : res.cursor;
  } while (cursor);
}

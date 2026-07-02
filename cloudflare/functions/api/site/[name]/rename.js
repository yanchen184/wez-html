// POST /api/site/<name>/rename   改站名(需 admin token)
// body: { identity, new_site }
// 對齊 wez-html renameSite:只有原上傳者能改;撞名回 409;檔案 + KV data 一併搬。

import {
  SITE_RE,
  META_PREFIX,
  FILE_PREFIX,
  DATA_PREFIX,
  json,
  err,
  requireAdmin,
} from "../../../_shared.js";

const IDENTITY_RE = /^[a-zA-Z0-9_-]{1,20}$/;

export async function onRequest(context) {
  const { params, request, env } = context;
  const site = params.name;

  if (request.method !== "POST") return err("POST only", 405);
  if (!SITE_RE.test(site)) return err("bad site name", 400);

  const authErr = requireAdmin(request, env);
  if (authErr) return err(authErr, authErr === "unauthorized" ? 401 : 500);

  let body = {};
  try {
    body = await request.json();
  } catch {
    return err("bad json", 400);
  }
  const identity = (body.identity || "").trim();
  const newSite = (body.new_site || "").trim();
  if (!IDENTITY_RE.test(identity)) return err("bad identity", 400);
  if (!SITE_RE.test(newSite))
    return err("new_site must match ^[a-z0-9-]{1,40}$", 400);

  const metaRaw = await env.SITES.get(META_PREFIX + site);
  if (!metaRaw) return err("not found", 404);
  const meta = JSON.parse(metaRaw);
  if (meta.uploader !== identity)
    return err(`only original uploader (${meta.uploader}) can rename`, 403);

  const origin = new URL(request.url).origin;
  if (newSite === site) {
    return json({ status: "ok", site, url: `${origin}/${site}/` });
  }

  // 撞名檢查
  const clash = await env.SITES.get(META_PREFIX + newSite);
  if (clash) {
    return json(
      { status: "conflict", site: newSite, hint: "pick another site name" },
      409
    );
  }

  // 搬 file:<site>/* 與 data:<site>/* 到新前綴
  await movePrefix(env.SITES, FILE_PREFIX + site + "/", FILE_PREFIX + newSite + "/");
  await movePrefix(env.SITES, DATA_PREFIX + site + "/", DATA_PREFIX + newSite + "/");

  // 更新 meta 並搬 key
  meta.site = newSite;
  await env.SITES.put(META_PREFIX + newSite, JSON.stringify(meta));
  await env.SITES.delete(META_PREFIX + site);

  return json({ status: "ok", site: newSite, url: `${origin}/${newSite}/` });
}

// 把 fromPrefix 底下的所有 key 複製到 toPrefix 對應位置,再刪舊 key。
async function movePrefix(kv, fromPrefix, toPrefix) {
  let cursor;
  do {
    const res = await kv.list({ prefix: fromPrefix, cursor });
    for (const k of res.keys) {
      const val = await kv.get(k.name);
      if (val !== null) {
        const suffix = k.name.slice(fromPrefix.length);
        await kv.put(toPrefix + suffix, val);
      }
      await kv.delete(k.name);
    }
    cursor = res.list_complete ? null : res.cursor;
  } while (cursor);
}

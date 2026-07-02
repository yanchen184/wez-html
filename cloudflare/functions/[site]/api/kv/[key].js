// 站台級 KV：前端 demo 資料用（scoreboard / poll / 留言板…）
// GET    /<site>/api/kv/<key>   讀
// PUT    /<site>/api/kv/<key>   寫（body 必須是合法 JSON）
// DELETE /<site>/api/kv/<key>   刪
//
// 注意：這層「不需要 admin token」——同站台的人都讀寫得到（沿用 wez-html 的信任模型）。
// 真正受保護的是「上傳/刪整個站台」（走 /api/admin）。

import {
  SITE_RE,
  KEY_RE,
  LIMITS,
  META_PREFIX,
  DATA_PREFIX,
  json,
  err,
} from "../../../_shared.js";

export async function onRequest(context) {
  const { params, request, env } = context;
  const site = params.site;
  const key = params.key;

  if (!SITE_RE.test(site)) return err("bad site name", 400);
  if (!KEY_RE.test(key)) return err("key must match ^[a-zA-Z0-9_-]{1,64}$", 400);

  // 站台必須存在才允許讀寫它的 KV
  const meta = await env.SITES.get(META_PREFIX + site);
  if (!meta) return err("site not found", 404);

  const kvKey = DATA_PREFIX + site + "/" + key;

  if (request.method === "GET") {
    const val = await env.SITES.get(kvKey);
    if (val === null) return err("key not found", 404);
    return new Response(val, {
      headers: {
        "Content-Type": "application/json; charset=utf-8",
        "Cache-Control": "no-store",
      },
    });
  }

  if (request.method === "PUT") {
    const body = await request.text();
    if (body.length > LIMITS.KV_VALUE_MAX)
      return err(`value too large (max ${LIMITS.KV_VALUE_MAX} bytes)`, 413);
    try {
      JSON.parse(body);
    } catch {
      return err("body must be valid JSON", 400);
    }
    await env.SITES.put(kvKey, body);
    return json({ ok: true, key });
  }

  if (request.method === "DELETE") {
    await env.SITES.delete(kvKey);
    return json({ ok: true, key });
  }

  return err("method not allowed", 405);
}

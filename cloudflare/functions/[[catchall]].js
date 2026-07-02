// Catch-all：服務站台內容 /<site>/<path...>
//
// Pages Functions routing：更具體的 function 檔（/api/*、/<site>/api/kv/*）會優先匹配，
// 這支只接「沒被更具體 function 接走」的請求。
// 首頁 / 由 public/index.html（靜態）服務，不會進這裡。

import {
  SITE_RE,
  META_PREFIX,
  FILE_PREFIX,
  contentType,
} from "./_shared.js";

export async function onRequest(context) {
  const { request, env, next } = context;
  const url = new URL(request.url);
  const path = url.pathname;

  // 根路徑與 /api/* 不歸這支管：交回 Pages（靜態 index.html / 其他 function）
  if (path === "/" || path === "" || path.startsWith("/api/")) {
    return next();
  }

  const parts = path.replace(/^\//, "").split("/");
  const site = parts[0];
  if (!SITE_RE.test(site)) return next();

  // 站台必須存在
  const metaRaw = await env.SITES.get(META_PREFIX + site);
  if (!metaRaw) return notFound(site);

  // /<site> （沒結尾斜線）→ 301 補斜線
  if (parts.length === 1 && !path.endsWith("/")) {
    return Response.redirect(url.origin + "/" + site + "/", 301);
  }

  let rest = parts.slice(1).join("/");
  if (rest === "" || rest.endsWith("/")) rest += "index.html";

  // /<site>/api/* 預留給 KV function 與未來：不存在的 api 路徑直接 404
  if (rest.startsWith("api/")) return notFound(site);

  const fileKey = FILE_PREFIX + site + "/" + rest;
  let content = await env.SITES.get(fileKey);

  // SPA fallback：非 asset（沒副檔名或 .html）找不到 → 回站台 index.html
  if (content === null) {
    const isAsset = /\.[a-z0-9]+$/i.test(rest) && !/\.html?$/i.test(rest);
    if (!isAsset) {
      content = await env.SITES.get(FILE_PREFIX + site + "/index.html");
    }
    if (content === null) return notFound(site);
  }

  return new Response(content, {
    headers: {
      "Content-Type": contentType(rest),
      "Cache-Control": "no-cache",
    },
  });
}

function notFound(site) {
  return new Response(
    `<!doctype html><meta charset=utf-8><title>404</title>` +
      `<body style="font-family:system-ui;padding:40px;color:#333">` +
      `<h1 style="color:#dc2626">404</h1><p>站台 <code>${site}</code> 不存在或檔案找不到。</p>` +
      `<p><a href="/">← 回首頁</a></p>`,
    { status: 404, headers: { "Content-Type": "text/html; charset=utf-8" } }
  );
}

// 共用工具：給所有 Pages Functions 用。
// 設計取向沿用 wez-html：站名 / key 嚴格 regex、JSON 統一回應、size 上限。

export const SITE_RE = /^[a-z0-9-]{1,40}$/;
export const KEY_RE = /^[a-zA-Z0-9_-]{1,64}$/;

// 單一檔案路徑（站台內）規則：避免路徑穿越，只允許安全字元 + /
export const FILE_RE = /^[a-zA-Z0-9._\-/]{1,200}$/;

export const LIMITS = {
  // Workers KV 單值上限 25MB，但 demo 等級設小一點避免誤用
  KV_VALUE_MAX: 256 * 1024, // 256KB per data key
  FILE_MAX: 2 * 1024 * 1024, // 2MB per uploaded file（KV value 上限內）
  SITE_KEYS_MAX: 1000, // 一站最多 1000 個 data key
};

// 站台 metadata 在 KV 的 key 前綴
export const META_PREFIX = "meta:"; // meta:<site>  -> { uploader, created, files: [...] }
export const FILE_PREFIX = "file:"; // file:<site>/<path> -> 檔案內容
export const DATA_PREFIX = "data:"; // data:<site>/<key>  -> 站台級 KV value（前端用）

export function json(data, status = 200, extraHeaders = {}) {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "no-store",
      ...extraHeaders,
    },
  });
}

export function err(message, status = 400) {
  return json({ error: message }, status);
}

// admin token 驗證：公網必須有。token 存在 Pages 環境變數 ADMIN_TOKEN。
export function requireAdmin(request, env) {
  const expected = env.ADMIN_TOKEN;
  if (!expected) return "server missing ADMIN_TOKEN";
  const got =
    request.headers.get("X-Admin-Token") ||
    new URL(request.url).searchParams.get("token");
  if (got !== expected) return "unauthorized";
  return null; // ok
}

// 依副檔名給 content-type（serveSite 用）
const CT = {
  html: "text/html; charset=utf-8",
  htm: "text/html; charset=utf-8",
  css: "text/css; charset=utf-8",
  js: "text/javascript; charset=utf-8",
  mjs: "text/javascript; charset=utf-8",
  json: "application/json; charset=utf-8",
  svg: "image/svg+xml",
  png: "image/png",
  jpg: "image/jpeg",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  ico: "image/x-icon",
  txt: "text/plain; charset=utf-8",
  woff: "font/woff",
  woff2: "font/woff2",
};

export function contentType(path) {
  const ext = path.split(".").pop().toLowerCase();
  return CT[ext] || "application/octet-stream";
}

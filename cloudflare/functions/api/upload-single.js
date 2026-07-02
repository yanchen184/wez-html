// POST /api/upload-single   單檔 .html 上傳（需 admin token）
// multipart form: file=<.html>, site, identity, force=0|1
//
// 對齊 wez-html 行為：撞名回 409 + existing_uploader；force=1 覆蓋但保留站台 KV data。
// 公網強化：必須帶 X-Admin-Token（或 ?token=），否則 401。

import {
  SITE_RE,
  KEY_RE,
  LIMITS,
  META_PREFIX,
  FILE_PREFIX,
  json,
  err,
  requireAdmin,
  normalizeProjectName,
} from "../_shared.js";

const IDENTITY_RE = /^[a-zA-Z0-9_-]{1,20}$/;

export async function onRequest(context) {
  const { request, env } = context;
  if (request.method !== "POST") return err("POST only", 405);

  const authErr = requireAdmin(request, env);
  if (authErr) return err(authErr, authErr === "unauthorized" ? 401 : 500);

  const form = await request.formData();
  const site = (form.get("site") || "").trim();
  const identity = (form.get("identity") || "").trim();
  const force = form.get("force") === "1";
  const file = form.get("file");

  if (!SITE_RE.test(site)) return err("site must match ^[a-z0-9-]{1,40}$", 400);
  if (!IDENTITY_RE.test(identity))
    return err("identity must match ^[a-zA-Z0-9_-]{1,20}$", 400);
  if (!file || typeof file === "string") return err("missing file", 400);

  let projectName;
  try {
    projectName = normalizeProjectName(form.get("project_name") || "");
  } catch (e) {
    return err(e.message, 400);
  }

  const name = file.name || "";
  if (!/\.(html|htm)$/i.test(name))
    return err("only .html / .htm allowed in single-file mode", 400);
  if (file.size > LIMITS.FILE_MAX)
    return err(`file too big (max ${LIMITS.FILE_MAX / 1024 / 1024}MB)`, 413);

  const metaKey = META_PREFIX + site;
  const existingRaw = await env.SITES.get(metaKey);
  let existingMeta = null;
  if (existingRaw) {
    existingMeta = JSON.parse(existingRaw);
    if (!force) {
      return json(
        {
          status: "conflict",
          site,
          existing_uploader: existingMeta.uploader,
          hint: "use force=1 to overwrite, or pick another site name",
        },
        409
      );
    }
  }

  // force 覆蓋時,若新上傳未帶 project_name,沿用舊站台的設定
  if (projectName === "" && existingMeta) {
    projectName = existingMeta.project_name || "";
  }

  const content = await file.text();
  const now = new Date().toISOString();

  // 單檔站台：固定一個 index.html
  await env.SITES.put(FILE_PREFIX + site + "/index.html", content);

  const meta = {
    project_name: projectName,
    site,
    uploader: identity,
    uploaded_at: now,
    src: name,
    src_path: (form.get("src_path") || "").trim(),
    size_bytes: file.size,
    files: ["index.html"],
  };
  await env.SITES.put(metaKey, JSON.stringify(meta));

  const origin = new URL(request.url).origin;
  return json({
    status: "ok",
    site,
    url: `${origin}/${site}/`,
    uploader: identity,
    size_bytes: file.size,
    files: 1,
  });
}

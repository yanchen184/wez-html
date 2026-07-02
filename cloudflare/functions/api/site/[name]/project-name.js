// POST /api/site/<name>/project-name   改專案名(需 admin token)
// body: { identity, project_name }
// 對齊 wez-html updateProjectName:只有原上傳者能改;project_name 單行、最多 80 字元;留空可清除。

import {
  SITE_RE,
  META_PREFIX,
  json,
  err,
  requireAdmin,
  normalizeProjectName,
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
  if (!IDENTITY_RE.test(identity)) return err("bad identity", 400);

  let projectName;
  try {
    projectName = normalizeProjectName(body.project_name || "");
  } catch (e) {
    return err(e.message, 400);
  }

  const metaRaw = await env.SITES.get(META_PREFIX + site);
  if (!metaRaw) return err("not found", 404);
  const meta = JSON.parse(metaRaw);
  if (meta.uploader !== identity)
    return err(
      `only original uploader (${meta.uploader}) can update project name`,
      403
    );

  meta.project_name = projectName;
  await env.SITES.put(META_PREFIX + site, JSON.stringify(meta));

  return json({ status: "ok", site, project_name: projectName });
}

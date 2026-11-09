import { readFile, rm, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { type NextRequest } from "next/server";
import { sameOrigin, SESSION_COOKIE, validSession } from "@/lib/auth";

const root = process.env.CNC_WORDPRESS_GENERATOR_ROOT || "/opt/cnc/wordpress-generator";
const pauseFile = join(root, "paused");

function signedIn(request: NextRequest) {
  return validSession(request.cookies.get(SESSION_COOKIE)?.value);
}

async function status() {
  const metrics = await readFile(join(root, "status.json"), "utf8");
  const enabled = await stat(pauseFile).then(() => false, (error: NodeJS.ErrnoException) => {
    if (error.code === "ENOENT") return true;
    throw error;
  });
  const data = JSON.parse(metrics);
  return Response.json({
    enabled,
    active_job: data.active_job,
    merged_verified: data.merged_verified,
    hostnames_per_second: data.hostnames_per_second,
    updated_at: data.updated_at,
  }, { headers: { "Cache-Control": "no-store" } });
}

export async function GET(request: NextRequest) {
  if (!signedIn(request)) return Response.json({ error: "Please sign in." }, { status: 401 });
  try {
    return await status();
  } catch {
    return Response.json({ error: "Generator status unavailable." }, { status: 503 });
  }
}

export async function POST(request: NextRequest) {
  if (!signedIn(request)) return Response.json({ error: "Please sign in." }, { status: 401 });
  if (!sameOrigin(request)) return Response.json({ error: "Use the CNC dashboard to change this setting." }, { status: 403 });
  const body = await request.json().catch(() => null);
  if (typeof body?.enabled !== "boolean") {
    return Response.json({ error: "Expected an enabled boolean." }, { status: 400 });
  }
  try {
    if (body.enabled) await rm(pauseFile, { force: true });
    else await writeFile(pauseFile, "", { mode: 0o600 });
    return await status();
  } catch {
    return Response.json({ error: "Generator control unavailable." }, { status: 503 });
  }
}

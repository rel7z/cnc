import { readFile, stat } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { join } from "node:path";
import { type NextRequest } from "next/server";
import { sameOrigin, SESSION_COOKIE, validSession } from "@/lib/auth";

const root = process.env.CNC_WORDPRESS_GENERATOR_ROOT || "/opt/cnc/wordpress-generator";
const pauseFile = join(root, "paused");
const merged = process.env.CNC_WORDPRESS_MERGED_ROOT || "/opt/cnc/merged";
const execute = promisify(execFile);
export const runtime = "nodejs";

function signedIn(request: NextRequest) {
  return validSession(request.cookies.get(SESSION_COOKIE)?.value);
}

async function mapRequest(path: string, body?: unknown) {
  const origin = new URL(process.env.INTERNET_MAP_API_URL!);
  if (origin.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) || origin.username || origin.password) throw new Error("Internet Map requires loopback");
  const key = process.env.INTERNET_MAP_API_KEY;
  if (!key) throw new Error("Internet Map API key unavailable");
  const result = await fetch(new URL(path, origin), {
    method: body === undefined ? "GET" : "POST",
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
    redirect: "error", cache: "no-store", signal: AbortSignal.timeout(60_000),
  });
  return Response.json(await result.json(), { status: result.status, headers: { "Cache-Control": "no-store" } });
}

async function status() {
  const metrics = await readFile(join(root, "status.json"), "utf8");
  const pausedAt = await stat(pauseFile).then((info) => Math.floor(info.mtimeMs / 1000), (error: NodeJS.ErrnoException) => {
    if (error.code === "ENOENT") return null;
    throw error;
  });
  const data = JSON.parse(metrics);
  const session = await readFile(join(root, "output-session.json"), "utf8")
    .then((text) => JSON.parse(text), (error: NodeJS.ErrnoException) => {
      if (error.code === "ENOENT") return null;
      throw error;
    });
  const sources = await readFile(join(root, "sources.json"), "utf8").then(text => JSON.parse(text), (error: NodeJS.ErrnoException) => {
    if (error.code === "ENOENT") return { commoncrawl: true, reverse_ip: true, ct: true, other_catalogs: true };
    throw error;
  });
  const mode = await readFile(join(root, "commoncrawl-mode.json"), "utf8").then(text => JSON.parse(text).mode, (error: NodeJS.ErrnoException) => {
    if (error.code === "ENOENT") return "wat";
    throw error;
  });
  return Response.json({
    commoncrawl_mode: mode,
    commoncrawl_max_age_years: 2,
    index_status: data.index_status,
    sources,
    pool_available: data.pool_available,
    pool_target: data.pool_target,
    shards_per_worker: data.shards_per_worker,
    eligible_workers: data.eligible_workers,
    enabled: pausedAt === null,
    paused_at: pausedAt,
    ready_shards: data.ready_shards,
    published_per_10s: data.published_per_10s,
    active_job: data.active_job,
    merged_verified: session?.session_verified ?? data.merged_verified,
    total_verified: data.total_verified ?? data.merged_verified,
    output_file: session?.output_file ?? "wordpress.txt",
    last_archive: session?.last_archive ?? null,
    output_session: session?.output_session ?? null,
    hostnames_per_second: data.hostnames_per_second,
    updated_at: data.updated_at,
  }, { headers: { "Cache-Control": "no-store" } });
}

export async function GET(request: NextRequest) {
  if (!signedIn(request)) return Response.json({ error: "Please sign in." }, { status: 401 });
  try {
    if (process.env.INTERNET_MAP_API_URL) return await mapRequest("/api/v1/discovery/status");
    return await status();
  } catch {
    return Response.json({ error: "Generator status unavailable." }, { status: 503 });
  }
}

export async function POST(request: NextRequest) {
  if (!signedIn(request)) return Response.json({ error: "Please sign in." }, { status: 401 });
  if (!sameOrigin(request)) return Response.json({ error: "Use the CNC dashboard to change this setting." }, { status: 403 });
  const body = await request.json().catch(() => null);
  if (process.env.INTERNET_MAP_API_URL) {
    try { return await mapRequest("/api/v1/discovery/control", body); }
    catch { return Response.json({ error: "Internet Map coordinator unavailable." }, { status: 503 }); }
  }
  if (body?.commoncrawl_mode !== undefined) {
    if (!["index", "wat"].includes(body.commoncrawl_mode)) return Response.json({ error: "Choose Index or WAT." }, { status: 400 });
    try {
      await execute("/usr/bin/python3", [join(root, "wordpress_sources.py"), "--root", root, "--set-mode", body.commoncrawl_mode], { timeout: 10_000, maxBuffer: 64 * 1024 });
      return await status();
    } catch { return Response.json({ error: "Could not save Common Crawl mode." }, { status: 503 }); }
  }
  if (body?.sources && typeof body.sources === "object" && !Array.isArray(body.sources)) {
    const keys = Object.keys(body.sources);
    if (!keys.length || keys.some(key => !["commoncrawl", "reverse_ip", "ct", "other_catalogs"].includes(key) || typeof body.sources[key] !== "boolean")) {
      return Response.json({ error: "Expected source switches." }, { status: 400 });
    }
    try {
      await execute("/usr/bin/python3", [join(root, "wordpress_sources.py"), "--root", root, "--set-json", JSON.stringify(body.sources)], { timeout: 10_000, maxBuffer: 64 * 1024 });
      return await status();
    } catch { return Response.json({ error: "Could not save source switches." }, { status: 503 }); }
  }
  if (typeof body?.enabled !== "boolean") {
    return Response.json({ error: "Expected an enabled boolean." }, { status: 400 });
  }
  try {
    await execute("/usr/bin/python3", [
      join(root, "wordpress_output.py"), "--root", root, "--merged", merged,
      "--control", body.enabled ? "on" : "off",
    ], { timeout: 60_000, maxBuffer: 64 * 1024 });
    return await status();
  } catch {
    return Response.json({ error: "Generator control unavailable." }, { status: 503 });
  }
}

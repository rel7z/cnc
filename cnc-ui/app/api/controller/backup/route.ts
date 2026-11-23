import { type NextRequest } from "next/server";
import { sameOrigin, SESSION_COOKIE, validSession } from "@/lib/auth";

export const runtime = "nodejs";

async function requestBackup(create: boolean) {
  try {
    const origin = new URL(process.env.INTERNET_MAP_API_URL || "http://127.0.0.1:3500");
    if (origin.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) || origin.username || origin.password) throw new Error("Invalid backup connection");
    const key = process.env.INTERNET_MAP_API_KEY;
    if (!key) throw new Error("Backup connection unavailable");
    const response = await fetch(new URL("/api/v1/database/bundle/" + (create ? "create" : "status"), origin), {
      method: create ? "POST" : "GET",
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
      body: create ? "{}" : undefined,
      cache: "no-store", redirect: "error", signal: AbortSignal.timeout(15_000),
    });
    const data = await response.json();
    if (create) return Response.json(data, { status: response.status, headers: { "Cache-Control": "no-store" } });
    return Response.json({
      running: !!data.is_backup_running,
      latest: data.backup?.data?.filename ? {
        filename: data.backup.data.filename, bytes: data.backup.data.bytes,
        updated_at: data.backup.updated_at, uploaded: !!data.drive?.is_uploaded,
      } : null,
    }, { status: response.status, headers: { "Cache-Control": "no-store" } });
  } catch {
    return Response.json({ error: "Backup controller belum tersedia." }, { status: 503 });
  }
}

export async function GET(request: NextRequest) {
  if (!validSession(request.cookies.get(SESSION_COOKIE)?.value)) return Response.json({ error: "Please sign in." }, { status: 401 });
  return requestBackup(false);
}

export async function POST(request: NextRequest) {
  if (!validSession(request.cookies.get(SESSION_COOKIE)?.value)) return Response.json({ error: "Please sign in." }, { status: 401 });
  if (!sameOrigin(request)) return Response.json({ error: "Gunakan tombol backup di controller." }, { status: 403 });
  return requestBackup(true);
}

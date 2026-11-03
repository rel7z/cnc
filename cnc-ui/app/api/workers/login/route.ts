import { type NextRequest } from "next/server";
import { sameOrigin, SESSION_COOKIE, validSession } from "@/lib/auth";

export async function POST(request: NextRequest) {
  const headers = { "Cache-Control": "no-store" };
  if (!validSession(request.cookies.get(SESSION_COOKIE)?.value)) return Response.json({ error: "Please sign in to continue." }, { status: 401, headers });
  if (!sameOrigin(request)) return Response.json({ error: "Worker login requests must come from this dashboard." }, { status: 403, headers });
  const reader = request.body?.getReader();
  let body = "";
  if (reader) {
    const chunks: Uint8Array[] = []; let size = 0;
    try {
      for (;;) { const { value, done } = await reader.read(); if (done) break; size += value.byteLength; if (size > 4096) { await reader.cancel(); return Response.json({ error: "Request is too large." }, { status: 413, headers }); } chunks.push(value); }
      body = Buffer.concat(chunks).toString("utf8");
    } finally { reader.releaseLock(); }
  }
  try {
    const response = await fetch(`${process.env.GO_API_URL || "http://localhost:8080"}/api/workers/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body, cache: "no-store", redirect: "error", signal: AbortSignal.any([request.signal, AbortSignal.timeout(10000)]) });
    return new Response(response.body, { status: response.status, headers: { ...headers, "Content-Type": "application/json" } });
  } catch { return Response.json({ error: "Worker login storage is unavailable." }, { status: 503, headers }); }
}

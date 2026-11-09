import { type NextRequest } from "next/server";
import { sameOrigin, SESSION_COOKIE, validSession } from "@/lib/auth";

export async function proxyProgramPriorities(request: NextRequest, id?: string) {
  if (!validSession(request.cookies.get(SESSION_COOKIE)?.value)) return Response.json({ error: "Please sign in to continue." }, { status: 401 });
  if (request.method !== "GET" && !sameOrigin(request)) return Response.json({ error: "Priority changes must come from this dashboard." }, { status: 403 });
  const token = process.env.CNC_PROCESSOR_TOKEN;
  if (!token || token.length < 32) return Response.json({ error: "Priority storage is not configured." }, { status: 503 });
  if (id && !/^[a-zA-Z0-9_.-]{1,128}$/.test(id)) return Response.json({ error: "Invalid tool ID." }, { status: 400 });
  let body: Uint8Array | undefined;
  if (request.method !== "GET") {
    const reader = request.body?.getReader(); const parts: Uint8Array[] = []; let size = 0;
    if (reader) {
      try {
        for (;;) { const { done, value } = await reader.read(); if (done) break; size += value.byteLength; if (size > 1024) { await reader.cancel(); return Response.json({ error: "Priority request exceeds 1 KiB." }, { status: 413 }); } parts.push(value); }
      } finally { reader.releaseLock(); }
    }
    body = new Uint8Array(size); let offset = 0; for (const part of parts) { body.set(part, offset); offset += part.byteLength; }
  }
  try {
    const response = await fetch(`http://127.0.0.1:9191/v1/program-priorities${id ? "/" + encodeURIComponent(id) : ""}`, {
      method: request.method, headers: { Authorization: "Bearer " + token, "Content-Type": "application/json" },
      ...(body ? { body: Buffer.from(body) } : {}), cache: "no-store", redirect: "error",
      signal: AbortSignal.any([request.signal, AbortSignal.timeout(10000)]),
    });
    return new Response(response.body, { status: response.status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" } });
  } catch { return Response.json({ error: "Priority storage is unavailable. Your edits are still in the form." }, { status: 503 }); }
}

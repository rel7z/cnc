import { type NextRequest } from "next/server";
import { credentials, sameOrigin, SESSION_COOKIE, validSession } from "@/lib/auth";
import { boundedLaunchBody, dashboardAuditSignature } from "@/lib/enum-audit";

export async function POST(request: NextRequest) {
  if (!validSession(request.cookies.get(SESSION_COOKIE)?.value)) return Response.json({ error: "Please sign in to continue." }, { status: 401 });
  if (!sameOrigin(request)) return Response.json({ error: "Launch requests must come from this dashboard." }, { status: 403 });
  const secret = process.env.CNC_DASHBOARD_AUDIT_SECRET || process.env.CNC_SESSION_SECRET;
  if (!secret || secret.length < 32) return Response.json({ error: "Dashboard audit signing is not configured." }, { status: 503 });
  let body: Uint8Array;
  try { body = await boundedLaunchBody(request); } catch { return Response.json({ error: "Launch payload exceeds size limit." }, { status: 413 }); }
  const actor = credentials().username, timestamp = Date.now().toString();
  const upstream = await fetch(`${process.env.GO_API_URL || "http://localhost:8080"}/api/tools/launch`, {
    method: "POST", body: Buffer.from(body), signal: request.signal,
    headers: { "Content-Type": "application/json", "X-CNC-Actor": actor, "X-CNC-Audit-Time": timestamp, "X-CNC-Audit-Signature": dashboardAuditSignature(secret, actor, timestamp, body) },
  });
  return new Response(upstream.body, { status: upstream.status, headers: { "Content-Type": "application/json", "Cache-Control": "no-store" } });
}

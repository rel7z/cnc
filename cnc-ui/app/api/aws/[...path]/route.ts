import { sameOrigin } from "@/lib/auth";

type Context = { params: Promise<{ path: string[] }> };

async function forward(request: Request, context: Context) {
  if (request.method !== "GET" && !sameOrigin(request)) {
    return Response.json({ error: "Request origin does not match the dashboard" }, { status: 403 });
  }
  const { path } = await context.params;
  if (path.length !== 1 || !["settings", "instances", "quota", "workers"].includes(path[0])) {
    return Response.json({ error: "Unknown AWS endpoint" }, { status: 404 });
  }
  const base = process.env.GO_API_URL || "http://localhost:8080";
  try {
    const upstream = await fetch(`${base}/api/aws/${path[0]}`, {
      method: request.method,
      headers: request.method === "GET" ? undefined : { "Content-Type": "application/json" },
      body: request.method === "GET" ? undefined : await request.text(),
      cache: "no-store",
    });
    return new Response(await upstream.text(), {
      status: upstream.status,
      headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
    });
  } catch {
    return Response.json({ error: "AWS controller is unavailable" }, { status: 502 });
  }
}

export const GET = forward;
export const POST = forward;

import { type NextRequest } from "next/server";

// Server update streaming proxy — Next.js rewrites buffer the response by default,
// which prevents real-time terminal output during compilation. This route handler
// streams logs directly from the Go server to the browser without buffering.
export async function POST(req: NextRequest) {
  const goURL = process.env.GO_API_URL ?? "http://localhost:8080";

  try {
    const upstream = await fetch(`${goURL}/api/server/update`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      // @ts-expect-error — Node fetch supports duplex for streaming
      duplex: "half",
      signal: req.signal,
    });

    return new Response(upstream.body, {
      status: upstream.status,
      headers: {
        "Content-Type": "text/plain; charset=utf-8",
        "Cache-Control": "no-cache, no-transform",
        Connection: "keep-alive",
        "X-Accel-Buffering": "no",
      },
    });
  } catch (err: any) {
    return new Response(`[ERROR] Failed to connect to backend server: ${err.message}`, {
      status: 502,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
  }
}

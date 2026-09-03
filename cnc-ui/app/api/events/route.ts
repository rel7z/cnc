import { type NextRequest } from "next/server";

// SSE streaming proxy — Next.js rewrites buffer the response which breaks
// Server-Sent Events. This route handler streams bytes directly from the
// Go server to the browser without buffering.
export async function GET(req: NextRequest) {
  const goURL = process.env.GO_API_URL ?? "http://localhost:8080";

  const upstream = await fetch(`${goURL}/api/events`, {
    headers: { Accept: "text/event-stream" },
    // @ts-expect-error — Node fetch supports duplex for streaming
    duplex: "half",
    signal: req.signal,
  });

  return new Response(upstream.body, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  });
}

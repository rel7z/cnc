import { createHash, createHmac } from "node:crypto";

export function dashboardAuditSignature(secret: string, actor: string, timestamp: string, body: Uint8Array): string {
  const digest = createHash("sha256").update(body).digest("hex");
  return createHmac("sha256", secret).update(`${actor}\n${timestamp}\nPOST\n/api/tools/launch\n${digest}`).digest("hex");
}

export async function boundedLaunchBody(request: Request): Promise<Uint8Array> {
  if (!request.body) return new Uint8Array();
  const reader = request.body.getReader(), chunks: Uint8Array[] = [];
  let length = 0;
  try {
    for (;;) { const { done, value } = await reader.read(); if (done) break; length += value.length; if (length > 8 * 1024 * 1024) { await reader.cancel(); throw new Error("Launch payload exceeds size limit"); } chunks.push(value); }
  } finally { reader.releaseLock(); }
  const bytes = new Uint8Array(length); let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
  return bytes;
}

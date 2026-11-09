import { NextResponse } from "next/server";
import { createDriveConnectTicket, loadControllerDriveAuthorization } from "@/lib/drive-connect-ticket";

export const dynamic = "force-dynamic";

export async function POST() {
  try {
    const authorization = await loadControllerDriveAuthorization();
    const callbackValue = authorization.searchParams.get("redirect_uri");
    if (!callbackValue) throw new Error("Controller authorization URL has no callback.");
    const callback = new URL(callbackValue);
    const ticket = createDriveConnectTicket(authorization.href);
    const bridge = new URL("/drive-connect", callback.origin);
    bridge.searchParams.set("ticket", ticket);
    return NextResponse.json({ url: bridge.href }, { headers: { "Cache-Control": "no-store" } });
  } catch (error) {
    return new NextResponse(error instanceof Error ? error.message : "Could not start Google authorization.", {
      status: 502,
      headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" },
    });
  }
}

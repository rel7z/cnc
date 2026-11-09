import { NextRequest, NextResponse } from "next/server";
import { DRIVE_STATE_COOKIE, DRIVE_STATE_COOKIE_PATH, driveBridgeRequestURL, validateDriveBridge } from "@/lib/drive-connect";
import { loadControllerDriveAuthorization } from "@/lib/drive-connect-ticket";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  try {
    const authorization = await loadControllerDriveAuthorization();
    const state = authorization.searchParams.get("state");
    const browserURL = driveBridgeRequestURL(
      request.url,
      request.headers.get("x-forwarded-host") ?? request.headers.get("host"),
      request.headers.get("x-forwarded-proto"),
    );
    const bridge = validateDriveBridge(browserURL, authorization.href, state);
    const response = NextResponse.redirect(bridge.authorization);
    response.cookies.set(DRIVE_STATE_COOKIE, bridge.state, {
      httpOnly: true,
      sameSite: "lax",
      secure: bridge.secure,
      maxAge: 10 * 60,
      path: DRIVE_STATE_COOKIE_PATH,
    });
    response.headers.set("Cache-Control", "no-store");
    response.headers.set("Referrer-Policy", "no-referrer");
    return response;
  } catch (error) {
    return new NextResponse(error instanceof Error ? error.message : "Could not start Google authorization.", {
      status: 400,
      headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" },
    });
  }
}

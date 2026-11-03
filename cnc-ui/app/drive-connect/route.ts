import { NextRequest, NextResponse } from "next/server";
import { decodeDriveAuthorizationPayload, DRIVE_STATE_COOKIE, DRIVE_STATE_COOKIE_PATH, driveBridgeRequestURL, validateDriveBridge } from "@/lib/drive-connect";
import { consumeDriveConnectTicket } from "@/lib/drive-connect-ticket";

export function GET(request: NextRequest) {
  const recovery = () => new NextResponse(null, {
    status: 303,
    headers: { Location: "/drive-recover", "Cache-Control": "no-store", "Referrer-Policy": "no-referrer" },
  });
  try {
    const ticket = request.nextUrl.searchParams.get("ticket");
    const ticketAuthorization = ticket ? consumeDriveConnectTicket(ticket) : null;
    if (ticket && !ticketAuthorization) return recovery();
    const payload = request.nextUrl.searchParams.get("payload");
    const authorization = ticketAuthorization ?? (payload ? decodeDriveAuthorizationPayload(payload) : request.nextUrl.searchParams.get("authorization"));
    const state = authorization ? new URL(authorization).searchParams.get("state") : request.nextUrl.searchParams.get("state");
    const browserURL = driveBridgeRequestURL(
      request.url,
      request.headers.get("x-forwarded-host") ?? request.headers.get("host"),
      request.headers.get("x-forwarded-proto"),
    );
    const bridge = validateDriveBridge(
      browserURL,
      authorization,
      state,
    );
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
  } catch {
    return recovery();
  }
}

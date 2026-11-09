import { NextRequest, NextResponse } from "next/server";
import { SESSION_COOKIE, validSession } from "./lib/auth";

export function proxy(request: NextRequest) {
  const path=request.nextUrl.pathname;
  if (path === "/api/auth/login" || path === "/api/auth/logout") return NextResponse.next();
  // Google returns to a separate localhost origin. The controller still
  // requires a short-lived state cookie and consumes each state only once.
  if (path === "/api/config/gdrive/callback") return NextResponse.next();
  if (validSession(request.cookies.get(SESSION_COOKIE)?.value)) return NextResponse.next();
  if (path.startsWith("/api/")) return NextResponse.json({error:"Please sign in to continue."},{status:401,headers:{"Cache-Control":"no-store"}});
  const url=request.nextUrl.clone(); url.pathname="/login";
  url.search="";url.searchParams.set("next",path+request.nextUrl.search);
  return NextResponse.redirect(url);
}
export const config={matcher:["/dashboard/:path*","/api/:path*","/drive-local-start"]};

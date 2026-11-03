import { NextResponse } from "next/server";
import { sameOrigin, SESSION_COOKIE } from "@/lib/auth";
export async function POST(request: Request) {
  if (!sameOrigin(request)) return NextResponse.json({error:"Invalid request."},{status:403});
  const response=NextResponse.json({ok:true},{headers:{"Cache-Control":"no-store"}});
  response.cookies.set(SESSION_COOKIE,"",{httpOnly:true,sameSite:"lax",path:"/",maxAge:0});
  return response;
}

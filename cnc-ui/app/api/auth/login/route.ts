import { NextResponse } from "next/server";
import { checkCredentials, createSession, sameOrigin, SESSION_COOKIE, SESSION_SECONDS } from "@/lib/auth";

export async function POST(request: Request) {
  if (!sameOrigin(request)) return NextResponse.json({error:"Invalid request."},{status:403});
  if (Number(request.headers.get("content-length")) > 4096) return NextResponse.json({error:"Request too large."},{status:413});
  try {
    const text=await request.text();
    if (text.length>4096) return NextResponse.json({error:"Request too large."},{status:413});
    const body=JSON.parse(text);
    if (typeof body.username!=="string" || typeof body.password!=="string" || !checkCredentials(body.username,body.password)) {
      await new Promise(resolve=>setTimeout(resolve,400));
      return NextResponse.json({error:"Incorrect username or password."},{status:401});
    }
    let token: string;
    try { token=createSession(); } catch { return NextResponse.json({error:"Sign-in is not configured. Contact your administrator."},{status:503}); }
    const response=NextResponse.json({ok:true},{headers:{"Cache-Control":"no-store"}});
    response.cookies.set(SESSION_COOKIE,token,{httpOnly:true,sameSite:"lax",secure:request.headers.get("x-forwarded-proto")==="https",path:"/",maxAge:SESSION_SECONDS});
    return response;
  } catch { return NextResponse.json({error:"Invalid request."},{status:400}); }
}

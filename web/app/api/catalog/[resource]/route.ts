import { NextRequest } from "next/server";
export const dynamic="force-dynamic";
export async function GET(request:NextRequest,context:{params:Promise<{resource:string}>}){
 const {resource}=await context.params;
 if(!["stats","sites","observations"].includes(resource))return Response.json({error:"Unknown resource"},{status:404});
 const key=process.env.INTERNET_MAP_API_KEY;
 if(!key)return Response.json({error:"Configure the catalog API connection on the application server."},{status:503});
 try{
 const base=new URL(process.env.INTERNET_MAP_API_URL||"http://127.0.0.1:3500");
 if(base.protocol!=="https:"&&!(base.protocol==="http:"&&["127.0.0.1","localhost","[::1]"].includes(base.hostname)))throw Error("Invalid API origin");
 if(base.username||base.password)throw Error("Invalid API origin");
 const url=new URL("/api/v1/"+resource,base);
 for(const name of ["host","page","wordpress","url","limit"]){const value=request.nextUrl.searchParams.get(name);if(value!==null)url.searchParams.set(name,value);}
 const response=await fetch(url,{headers:{Authorization:"Bearer "+key},redirect:"error",cache:"no-store",signal:AbortSignal.any([request.signal,AbortSignal.timeout(10000)])});
 return new Response(response.body,{status:response.status,headers:{"Content-Type":"application/json","Cache-Control":"no-store"}});
 }catch{return Response.json({error:"Catalog service is unavailable."},{status:503});}
}

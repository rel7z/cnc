import { type NextRequest } from "next/server";
import {sameOrigin,SESSION_COOKIE,validSession} from "@/lib/auth";
const resources:Record<string,string>={templates:"processor-templates",results:"processor-results",assets:"processor-assets",setups:"processor-setups",reset:"checkpoints/reset"};
export async function proxyProcessorEditor(request:NextRequest,path:string[]){
 if(!validSession(request.cookies.get(SESSION_COOKIE)?.value))return Response.json({error:"Please sign in to continue."},{status:401});
 if(request.method!=="GET"&&!sameOrigin(request))return Response.json({error:"Changes must come from this dashboard."},{status:403});
 const [resource,id]=path;
 if(!resources[resource]||path.length>2||(id&&(!/^[a-zA-Z0-9-]{1,64}$/.test(id)||resource!=="templates")))return Response.json({error:"Unknown processor resource."},{status:404});
 const token=process.env.CNC_PROCESSOR_TOKEN;if(!token||token.length<32)return Response.json({error:"Processor connection is not configured."},{status:503});
 const limit=resource==='assets'?65*1024*1024:256*1024;
 let body:Uint8Array|undefined;
 if(request.method!=="GET"){
  const reader=request.body?.getReader();const parts:Uint8Array[]=[];let size=0;
  if(reader){try{for(;;){const {done,value}=await reader.read();if(done)break;size+=value.byteLength;if(size>limit){await reader.cancel();return Response.json({error:"Upload exceeds the allowed size."},{status:413});}parts.push(value);}}finally{reader.releaseLock();}}
  body=new Uint8Array(size);let offset=0;for(const part of parts){body.set(part,offset);offset+=part.byteLength;}
 }
 try{
  const url=new URL(`http://127.0.0.1:9191/v1/${resources[resource]}${id?'/'+encodeURIComponent(id):''}`);
  if(resource==='results')for(const key of ['name','run']){const value=request.nextUrl.searchParams.get(key);if(value)url.searchParams.set(key,value);}
  const response=await fetch(url,{method:request.method,headers:{Authorization:'Bearer '+token,'Content-Type':request.headers.get('content-type')||'application/json'},...(body?{body:Buffer.from(body)}:{}),cache:'no-store',redirect:'error',signal:AbortSignal.any([request.signal,AbortSignal.timeout(120000)])});
  const headers:Record<string,string>={'Content-Type':response.headers.get('content-type')||'application/json','Cache-Control':'no-store','X-Content-Type-Options':'nosniff'};
  const disposition=response.headers.get('content-disposition');if(disposition)headers['Content-Disposition']=disposition;
  return new Response(response.body,{status:response.status,headers});
 }catch{return Response.json({error:"Processor is unavailable. Your edits are still in the form."},{status:503});}
}

import {type NextRequest} from "next/server";
import {proxyProcessorEditor} from "@/lib/processor-editor-proxy";
type Context={params:Promise<{path:string[]}>};
async function handler(request:NextRequest,context:Context){return proxyProcessorEditor(request,(await context.params).path);}
export const GET=handler;
export const POST=handler;
export const PUT=handler;
export const DELETE=handler;

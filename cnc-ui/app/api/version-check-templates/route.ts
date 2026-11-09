import { type NextRequest } from "next/server";
import { proxyVersionTemplates } from "@/lib/version-template-proxy";
export const GET = (request: NextRequest) => proxyVersionTemplates(request);
export const POST = (request: NextRequest) => proxyVersionTemplates(request);

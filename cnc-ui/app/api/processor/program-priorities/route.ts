import { type NextRequest } from "next/server";
import { proxyProgramPriorities } from "@/lib/program-priority-proxy";
export const GET = (request: NextRequest) => proxyProgramPriorities(request);

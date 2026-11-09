import { type NextRequest } from "next/server";
import { proxyProgramPriorities } from "@/lib/program-priority-proxy";
export async function PUT(request: NextRequest, context: { params: Promise<{ id: string }> }) { return proxyProgramPriorities(request,(await context.params).id); }

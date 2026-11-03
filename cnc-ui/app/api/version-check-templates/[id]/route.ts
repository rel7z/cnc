import { type NextRequest } from "next/server";
import { proxyVersionTemplates } from "@/lib/version-template-proxy";
type Context = { params: Promise<{ id: string }> };
async function handle(request: NextRequest, context: Context) {
  const { id } = await context.params;
  return proxyVersionTemplates(request, id);
}
export { handle as GET, handle as PUT, handle as DELETE };

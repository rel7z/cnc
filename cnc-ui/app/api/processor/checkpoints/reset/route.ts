import { type NextRequest } from "next/server";
import { proxyProcessorEditor } from "@/lib/processor-editor-proxy";

export async function POST(request: NextRequest) {
  return proxyProcessorEditor(request, ["reset"]);
}

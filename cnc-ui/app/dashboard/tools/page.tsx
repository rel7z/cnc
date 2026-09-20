import { ToolsLauncher } from "@/components/dashboard/ToolsLauncher";

export const metadata = {
  title: "Tools | CNC Dashboard",
};

export default function ToolsPage() {
  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight text-white">Tools</h1>
      </div>

      <ToolsLauncher />
    </div>
  );
}

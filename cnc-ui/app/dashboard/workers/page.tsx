import { Header } from "@/components/dashboard/Header";
import { WorkerTable } from "@/components/dashboard/WorkerTable";

export default function WorkersPage() {
  return (
    <>
      <Header title="Workers" />
      <div className="p-6">
        <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
          <div className="px-5 py-3 border-b border-gray-800">
            <h2 className="text-sm font-semibold text-white">
              Connected Workers
            </h2>
            <p className="text-xs text-gray-500 mt-0.5">
              All workers registered with this server
            </p>
          </div>
          <WorkerTable />
        </div>
      </div>
    </>
  );
}

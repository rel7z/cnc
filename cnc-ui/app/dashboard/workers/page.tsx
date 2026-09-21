"use client";

import { useState } from "react";
import { Header } from "@/components/dashboard/Header";
import { WorkerTable } from "@/components/dashboard/WorkerTable";
import { DeployWorkerModal } from "@/components/dashboard/DeployWorkerModal";
import { Server } from "lucide-react";

export default function WorkersPage() {
  const [showDeploy, setShowDeploy] = useState(false);
  return (
    <>
      <Header title="Workers" />
      <div className="p-6">
        <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
          <div className="px-5 py-3 border-b border-gray-800 flex justify-between items-center">
            <div>
              <h2 className="text-sm font-semibold text-white">
                Connected Workers
              </h2>
              <p className="text-xs text-gray-500 mt-0.5">
                All workers registered with this server
              </p>
            </div>
            <button
              onClick={() => setShowDeploy(true)}
              className="flex items-center gap-2 px-3 py-1.5 bg-blue-600/10 text-blue-400 hover:bg-blue-600/20 border border-blue-500/20 rounded-md text-xs font-medium transition-colors"
            >
              <Server className="w-3.5 h-3.5" />
              Auto Deploy
            </button>
          </div>
          <WorkerTable />
        </div>
      </div>

      {showDeploy && (
        <DeployWorkerModal onClose={() => setShowDeploy(false)} />
      )}
    </>
  );
}

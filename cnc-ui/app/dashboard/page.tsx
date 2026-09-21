"use client";

import { useState } from "react";
import { Header } from "@/components/dashboard/Header";
import { WorkerTable } from "@/components/dashboard/WorkerTable";
import { SubmitJobForm } from "@/components/dashboard/SubmitJobForm";
import { GDriveStatus } from "@/components/dashboard/GDriveStatus";
import { GDriveSettings } from "@/components/dashboard/GDriveSettings";
import { TelegramStatus } from "@/components/dashboard/TelegramStatus";
import { TelegramSettings } from "@/components/dashboard/TelegramSettings";
import { DeployWorkerModal } from "@/components/dashboard/DeployWorkerModal";
import { Server } from "lucide-react";

export default function DashboardPage() {
  const [showDeploy, setShowDeploy] = useState(false);
  return (
    <>
      <Header title="Dashboard" />
      <div className="p-6 space-y-6">
        <GDriveStatus />
        <TelegramStatus />
        
        {/* Collapsible Integration Settings */}
        <div className="space-y-4">
          <GDriveSettings />
          <TelegramSettings />
        </div>
        
        <div className="bg-gray-900 border border-gray-800 rounded-xl">
          <div className="px-5 py-3 border-b border-gray-800 flex justify-between items-center">
            <h2 className="text-sm font-semibold text-white">Online Workers</h2>
            <button
              onClick={() => setShowDeploy(true)}
              className="flex items-center gap-2 px-3 py-1.5 bg-blue-600/10 text-blue-400 hover:bg-blue-600/20 border border-blue-500/20 rounded-md text-xs font-medium transition-colors"
            >
              <Server className="w-3.5 h-3.5" />
              Auto Deploy
            </button>
          </div>
          <div className="p-5">
            <WorkerTable />
          </div>
        </div>

        <div className="bg-gray-900 border border-gray-800 rounded-xl">
          <div className="px-5 py-3 border-b border-gray-800">
            <h2 className="text-sm font-semibold text-white">Submit Job</h2>
          </div>
          <div className="p-5">
            <SubmitJobForm />
          </div>
        </div>
      </div>

      {showDeploy && (
        <DeployWorkerModal onClose={() => setShowDeploy(false)} />
      )}
    </>
  );
}

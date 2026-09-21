"use client";

import { useState } from "react";
import { Header } from "@/components/dashboard/Header";
import { ServerTerminal } from "@/components/dashboard/ServerTerminal";
import { FileManager } from "@/components/dashboard/FileManager";
import { Terminal, Folder } from "lucide-react";

export default function ServerPage() {
  const [activeTab, setActiveTab] = useState<"terminal" | "files">("terminal");

  return (
    <div className="flex flex-col h-screen">
      <Header title="Server Management" />
      
      <div className="flex-1 flex flex-col p-6 overflow-hidden">
        {/* Tabs */}
        <div className="flex space-x-1 bg-gray-900 p-1 rounded-lg border border-gray-800 w-fit mb-4">
          <button
            onClick={() => setActiveTab("terminal")}
            className={`flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium transition-colors ${
              activeTab === "terminal"
                ? "bg-gray-800 text-white shadow-sm"
                : "text-gray-400 hover:text-white hover:bg-gray-800/50"
            }`}
          >
            <Terminal className="w-4 h-4" />
            Terminal
          </button>
          <button
            onClick={() => setActiveTab("files")}
            className={`flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium transition-colors ${
              activeTab === "files"
                ? "bg-gray-800 text-white shadow-sm"
                : "text-gray-400 hover:text-white hover:bg-gray-800/50"
            }`}
          >
            <Folder className="w-4 h-4" />
            File Manager
          </button>
        </div>

        {/* Content Area */}
        <div className="flex-1 overflow-hidden flex flex-col border border-gray-800 rounded-xl bg-gray-900 shadow-xl">
          {activeTab === "terminal" ? <ServerTerminal /> : <FileManager />}
        </div>
      </div>
    </div>
  );
}

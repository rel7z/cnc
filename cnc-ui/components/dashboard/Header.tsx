"use client";

import { useState } from "react";
import { LogOut } from "lucide-react";
import { useDashboard } from "@/components/providers/EventProvider";

interface HeaderProps {
  title: string;
}

function formatTime(date: Date | null): string {
  if (!date) return "—";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function Header({ title }: HeaderProps) {
  const { connected, lastUpdated } = useDashboard();
 const [leaving,setLeaving]=useState(false);
 const [logoutError,setLogoutError]=useState("");
 async function logout(){
  setLeaving(true);setLogoutError("");
  try {const r=await fetch("/api/auth/logout",{method:"POST"});if(!r.ok)throw new Error();window.location.assign("/login");}
  catch {setLogoutError("Sign-out failed. Please try again.");setLeaving(false);}
 }

  return (
    <header className="sticky top-0 z-10 bg-[#0a0a0f]/80 backdrop-blur-xl border-b border-gray-800/50 px-4 md:px-6 py-3 md:py-4 flex items-center justify-between shadow-sm">
      <h1 className="text-sm md:text-base font-semibold text-white tracking-wide">{title}</h1>
      <div className="flex items-center gap-3 md:gap-4">
 {logoutError && <span role="alert" className="text-xs text-red-300">{logoutError}</span>}
 <button onClick={logout} disabled={leaving} className="flex items-center gap-2 rounded-lg border border-gray-700 px-3 py-1.5 text-xs text-gray-300 hover:bg-gray-800 disabled:opacity-50"><LogOut size={14}/>{leaving?"Signing out…":"Sign out"}</button>
        {lastUpdated && (
          <span className="hidden md:inline-block text-xs text-gray-500 font-mono">
            Updated {formatTime(lastUpdated)}
          </span>
        )}
        <div className="flex items-center gap-2 bg-gray-900/50 px-2 py-1 md:px-3 md:py-1.5 rounded-full border border-gray-800/80">
          <span className="relative flex h-2 w-2 md:h-2.5 md:w-2.5">
            {connected && (
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
            )}
            <span
              className={`relative inline-flex rounded-full h-full w-full ${
                connected ? "bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.8)]" : "bg-gray-600"
              }`}
            />
          </span>
          <span
            className={`text-[10px] md:text-xs font-medium uppercase tracking-wider ${connected ? "text-emerald-400" : "text-gray-500"}`}
          >
            {connected ? "Live" : "Disconnected"}
          </span>
        </div>
      </div>
    </header>
  );
}

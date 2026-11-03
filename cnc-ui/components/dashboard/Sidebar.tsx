"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

interface NavItem {
  label: string;
  href: string;
  icon: React.ReactNode;
}

function GridIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} aria-hidden="true">
      <rect x="3" y="3" width="7" height="7" rx="1" />
      <rect x="14" y="3" width="7" height="7" rx="1" />
      <rect x="3" y="14" width="7" height="7" rx="1" />
      <rect x="14" y="14" width="7" height="7" rx="1" />
    </svg>
  );
}

function ServerIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} aria-hidden="true">
      <rect x="2" y="3" width="20" height="6" rx="1" />
      <rect x="2" y="15" width="20" height="6" rx="1" />
      <circle cx="6" cy="6" r="1" fill="currentColor" />
      <circle cx="6" cy="18" r="1" fill="currentColor" />
    </svg>
  );
}

function TerminalIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M4 6a2 2 0 012-2h12a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V6z" />
      <path strokeLinecap="round" strokeLinejoin="round" d="M8 10l2 2-2 2M13 14h3" />
    </svg>
  );
}

function BriefcaseIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} aria-hidden="true">
      <rect x="2" y="7" width="20" height="14" rx="2" />
      <path strokeLinecap="round" strokeLinejoin="round" d="M16 7V5a2 2 0 00-2-2h-4a2 2 0 00-2 2v2" />
    </svg>
  );
}

function ToolIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z" />
    </svg>
  );
}

const navItems: NavItem[] = [
  { label: "Dashboard", href: "/dashboard", icon: <GridIcon /> },
  { label: "Server", href: "/dashboard/server", icon: <TerminalIcon /> },
  { label: "Workers", href: "/dashboard/workers", icon: <ServerIcon /> },
  { label: "Jobs", href: "/dashboard/jobs", icon: <BriefcaseIcon /> },
  { label: "Tools", href: "/dashboard/tools", icon: <ToolIcon /> },
  { label: "Processor", href: "/dashboard/processor", icon: <ServerIcon /> },
];

export function Sidebar() {
  const pathname = usePathname();

  return (
    <nav
      className="w-full md:w-52 shrink-0 bg-[#0a0a0f]/95 md:bg-[#0a0a0f] backdrop-blur-xl border-t md:border-t-0 md:border-r border-gray-800/60 flex flex-row md:flex-col pb-[env(safe-area-inset-bottom)] z-50"
      aria-label="Main navigation"
    >
      {/* Logo */}
      <div className="hidden md:flex items-center gap-2.5 px-5 h-14 border-b border-gray-800/50">
        <div className="h-6 w-6 rounded bg-gradient-to-br from-blue-500 to-indigo-600 flex items-center justify-center text-xs font-bold text-white shadow-lg shadow-blue-500/20">
          C
        </div>
        <span className="text-sm font-semibold text-white tracking-wide">
          CNC
        </span>
        <span className="text-xs text-blue-400/80 font-medium tracking-wider uppercase">Dash</span>
      </div>

      {/* Nav links */}
      <ul className="grid grid-cols-6 md:flex md:flex-col gap-1 p-1.5 md:p-3 flex-1 md:justify-start w-full" role="list">
        {navItems.map((item) => {
          const isActive =
            item.href === "/dashboard"
              ? pathname === "/dashboard"
              : pathname.startsWith(item.href);
          return (
            <li key={item.href} className="min-w-0 md:flex-none">
              <Link
                href={item.href}
                className={`flex min-w-0 flex-col md:flex-row items-center justify-center md:justify-start gap-1 md:gap-2.5 px-1 md:px-3 py-2 md:py-2 rounded-lg text-[9px] sm:text-[10px] md:text-sm transition-colors ${
                  isActive
                    ? "bg-blue-500/10 md:bg-gray-800/80 text-blue-400 md:text-white font-semibold shadow-[0_0_15px_rgba(59,130,246,0.1)] md:shadow-none ring-1 ring-blue-500/20 md:ring-0"
                    : "text-gray-500 hover:text-gray-300 hover:bg-gray-800/30 font-medium"
                }`}
                aria-current={isActive ? "page" : undefined}
              >
                <div className={`transition-transform duration-300 ${isActive ? "scale-110" : ""}`}>
                  {item.icon}
                </div>
                <span className="max-w-full truncate">{item.label}</span>
              </Link>
            </li>
          );
        })}
      </ul>

      {/* Footer */}
      <div className="hidden md:block px-5 py-4 border-t border-gray-800/50 space-y-2">
        <a
          href="https://t.me/storefahrel7z7zbot"
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center gap-2.5 text-xs text-indigo-400 hover:text-indigo-300 transition-colors py-1 group bg-indigo-500/5 hover:bg-indigo-500/10 px-2 rounded-lg"
        >
          <svg className="h-4 w-4 text-indigo-400 group-hover:scale-110 transition-transform" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm4.64 6.8c-.15 1.58-.8 5.42-1.13 7.19-.14.75-.42 1-.68 1.03-.58.05-1.02-.38-1.58-.75-.88-.58-1.38-.94-2.23-1.5-.99-.65-.35-1.01.22-1.59.15-.15 2.71-2.48 2.76-2.69a.2.2 0 00-.05-.18c-.06-.05-.14-.03-.21-.02-.09.02-1.49.95-4.22 2.79-.4.27-.76.41-1.08.4-.36-.01-1.04-.2-1.55-.37-.63-.2-1.12-.31-1.08-.66.02-.18.27-.36.74-.55 2.92-1.27 4.86-2.11 5.83-2.51 2.78-1.16 3.35-1.36 3.73-1.36.08 0 .27.02.39.12.1.08.13.19.14.27-.01.06.01.24 0 .38z" />
          </svg>
          <span className="truncate font-medium">Telegram Bot</span>
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.8)] ml-auto animate-pulse" />
        </a>
        <div className="flex items-center gap-1.5 px-1 mt-3">
          <div className="h-1 w-1 bg-gray-600 rounded-full"></div>
          <p className="text-[10px] text-gray-500 font-mono tracking-wider">CNC v1.0 <span className="opacity-50">|</span> DISTRIBUTED</p>
        </div>
      </div>
    </nav>
  );
}

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

function BriefcaseIcon() {
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2} aria-hidden="true">
      <rect x="2" y="7" width="20" height="14" rx="2" />
      <path strokeLinecap="round" strokeLinejoin="round" d="M16 7V5a2 2 0 00-2-2h-4a2 2 0 00-2 2v2" />
    </svg>
  );
}

const navItems: NavItem[] = [
  { label: "Dashboard", href: "/dashboard", icon: <GridIcon /> },
  { label: "Workers", href: "/dashboard/workers", icon: <ServerIcon /> },
  { label: "Jobs", href: "/dashboard/jobs", icon: <BriefcaseIcon /> },
];

export function Sidebar() {
  const pathname = usePathname();

  return (
    <nav
      className="w-56 shrink-0 bg-gray-900 border-r border-gray-800 flex flex-col"
      aria-label="Main navigation"
    >
      {/* Logo */}
      <div className="flex items-center gap-2.5 px-5 h-14 border-b border-gray-800">
        <div className="h-6 w-6 rounded bg-blue-600 flex items-center justify-center text-xs font-bold text-white">
          C
        </div>
        <span className="text-sm font-semibold text-white tracking-wide">
          CNC
        </span>
        <span className="text-xs text-gray-500">Dashboard</span>
      </div>

      {/* Nav links */}
      <ul className="flex flex-col gap-0.5 p-3 flex-1" role="list">
        {navItems.map((item) => {
          const isActive =
            item.href === "/dashboard"
              ? pathname === "/dashboard"
              : pathname.startsWith(item.href);
          return (
            <li key={item.href}>
              <Link
                href={item.href}
                className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm transition-colors ${
                  isActive
                    ? "bg-gray-800 text-white font-medium"
                    : "text-gray-400 hover:text-white hover:bg-gray-800/50"
                }`}
                aria-current={isActive ? "page" : undefined}
              >
                {item.icon}
                {item.label}
              </Link>
            </li>
          );
        })}
      </ul>

      {/* Footer */}
      <div className="px-5 py-4 border-t border-gray-800">
        <p className="text-xs text-gray-600">CNC v1.0</p>
      </div>
    </nav>
  );
}

import Link from "next/link";
import { Header } from "@/components/dashboard/Header";
import { JobTable } from "@/components/dashboard/JobTable";

export default function JobsPage() {
  return (
    <>
      <Header title="Jobs" />
      <div className="p-6">
        <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
          <div className="flex items-center justify-between px-5 py-3 border-b border-gray-800">
            <div>
              <h2 className="text-sm font-semibold text-white">All Jobs</h2>
              <p className="text-xs text-gray-500 mt-0.5">
                Jobs submitted to this cluster
              </p>
            </div>
            <Link
              href="/dashboard/jobs/new"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-blue-600 hover:bg-blue-500 text-white text-xs font-medium transition-colors"
            >
              <svg
                className="h-3.5 w-3.5"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={2}
                aria-hidden="true"
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M12 4v16m8-8H4"
                />
              </svg>
              Submit Job
            </Link>
          </div>
          <JobTable />
        </div>
      </div>
    </>
  );
}

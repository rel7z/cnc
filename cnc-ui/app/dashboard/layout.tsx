import { fetchJobs, fetchStats, fetchWorkers, toRecord } from "@/lib/api";
import { EventProvider } from "@/components/providers/EventProvider";
import { Sidebar } from "@/components/dashboard/Sidebar";
import { ConnectionBanner } from "@/components/ui/ConnectionBanner";
import type { DashboardState } from "@/components/providers/EventProvider";

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // Fetch initial data server-side to pre-seed the EventProvider.
  // If the Go server is not running, gracefully fall back to empty state.
  let initialData: Partial<DashboardState> = {};
  try {
    const [stats, workers, jobs] = await Promise.all([
      fetchStats(),
      fetchWorkers(),
      fetchJobs(),
    ]);
    initialData = {
      stats,
      workers: toRecord(workers),
      jobs: toRecord(jobs),
    };
  } catch {
    // Server not reachable — EventProvider will populate via SSE once connected.
  }

  return (
    <EventProvider initialData={initialData}>
      <ConnectionBanner />
      <div className="flex h-screen overflow-hidden bg-gray-950">
        <Sidebar />
        <main className="flex-1 overflow-y-auto min-w-0">
          {children}
        </main>
      </div>
    </EventProvider>
  );
}

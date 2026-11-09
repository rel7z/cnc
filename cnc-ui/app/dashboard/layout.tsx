import { TelegramWebAppInit } from "@/components/providers/TelegramWebAppInit";
import { fetchJobs, fetchStats, fetchWorkers, fetchTasks, toRecord } from "@/lib/api";
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
    const [stats, workers, jobs, tasks] = await Promise.all([
      fetchStats(),
      fetchWorkers(),
      fetchJobs(),
      fetchTasks(),
    ]);
    initialData = {
      stats,
      workers: toRecord(workers),
      jobs: toRecord(jobs),
      tasks: toRecord(tasks),
    };
  } catch {
    // Server not reachable — EventProvider will populate via SSE once connected.
  }

  return (
    <EventProvider initialData={initialData}>
      <TelegramWebAppInit />
      <ConnectionBanner />
      <div className="flex flex-col-reverse md:flex-row h-[100dvh] overflow-hidden bg-[#0a0a0f]">
        <Sidebar />
        <main className="flex-1 overflow-y-auto min-w-0 bg-[#0a0a0f] pb-[env(safe-area-inset-bottom)]">
          {children}
        </main>
      </div>
    </EventProvider>
  );
}

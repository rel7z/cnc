import { Header } from "@/components/dashboard/Header";
import { StatCard } from "@/components/dashboard/StatCard";
import { WorkerSummary } from "@/components/dashboard/WorkerSummary";
import { JobSummary } from "@/components/dashboard/JobSummary";
import { StatsGrid } from "@/components/dashboard/StatsGrid";

export default function DashboardPage() {
  return (
    <>
      <Header title="Dashboard" />
      <div className="p-6 space-y-6">
        <StatsGrid />
        <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
          <WorkerSummary />
          <JobSummary />
        </div>
      </div>
    </>
  );
}

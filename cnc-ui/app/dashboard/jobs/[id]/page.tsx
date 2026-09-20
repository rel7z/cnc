import Link from "next/link";
import { notFound } from "next/navigation";
import { fetchJob } from "@/lib/api";
import { Header } from "@/components/dashboard/Header";
import { JobDetail } from "@/components/dashboard/JobDetail";

interface JobDetailPageProps {
  params: Promise<{ id: string }>;
}

export default async function JobDetailPage({ params }: JobDetailPageProps) {
  const { id } = await params;

  // Verify the job exists server-side so we can return a proper 404.
  // The full rendering (with live updates) is handled by JobDetail which
  // reads from the EventProvider context populated via SSE.
  try {
    await fetchJob(id);
  } catch {
    notFound();
  }

  return (
    <>
      <Header title="Job Detail" />
      <div className="px-6 pt-4">
        <nav
          className="flex items-center gap-1.5 text-xs text-gray-500"
          aria-label="Breadcrumb"
        >
          <Link href="/dashboard" className="hover:text-gray-300 transition-colors">
            Dashboard
          </Link>
          <span>/</span>
          <Link href="/dashboard/jobs" className="hover:text-gray-300 transition-colors">
            Jobs
          </Link>
          <span>/</span>
          <span className="text-gray-300">{id}</span>
        </nav>
      </div>
      <JobDetail jobId={id} />
    </>
  );
}

import { Skeleton } from "@/components/ui/Skeleton";

function StatCardSkeleton() {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl p-5 space-y-2">
      <Skeleton className="h-3 w-24" />
      <Skeleton className="h-9 w-16" />
      <Skeleton className="h-2.5 w-32" />
    </div>
  );
}

function TableSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
      <div className="px-5 py-3 border-b border-gray-800">
        <Skeleton className="h-4 w-24" />
      </div>
      <div className="divide-y divide-gray-800">
        {Array.from({ length: rows }).map((_, i) => (
          <div key={i} className="flex items-center gap-4 px-5 py-3">
            <Skeleton className="h-3 w-32" />
            <Skeleton className="h-5 w-16 rounded-full" />
            <Skeleton className="h-2 w-20 rounded-full" />
            <Skeleton className="h-3 w-16 ml-auto" />
          </div>
        ))}
      </div>
    </div>
  );
}

export default function DashboardLoading() {
  return (
    <div className="p-6 space-y-6">
      {/* Stat cards skeleton */}
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-4">
        <StatCardSkeleton />
        <StatCardSkeleton />
        <StatCardSkeleton />
        <StatCardSkeleton />
      </div>
      {/* Summary tables skeleton */}
      <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
        <TableSkeleton rows={3} />
        <TableSkeleton rows={3} />
      </div>
    </div>
  );
}

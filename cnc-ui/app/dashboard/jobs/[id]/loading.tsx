import { Skeleton } from "@/components/ui/Skeleton";

export default function JobDetailLoading() {
  return (
    <div className="p-6 space-y-6">
      {/* Breadcrumb */}
      <div className="flex items-center gap-1.5">
        <Skeleton className="h-3 w-16" />
        <span className="text-gray-700">/</span>
        <Skeleton className="h-3 w-10" />
        <span className="text-gray-700">/</span>
        <Skeleton className="h-3 w-24" />
      </div>

      {/* Job card */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl p-5 space-y-4">
        <div className="flex items-start justify-between">
          <div className="space-y-1.5">
            <Skeleton className="h-5 w-40" />
            <Skeleton className="h-3 w-56" />
          </div>
          <Skeleton className="h-5 w-20 rounded-full" />
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Skeleton className="h-10 w-full rounded" />
          <div className="space-y-2.5">
            {Array.from({ length: 5 }).map((_, i) => (
              <div key={i} className="flex justify-between">
                <Skeleton className="h-3 w-16" />
                <Skeleton className="h-3 w-20" />
              </div>
            ))}
          </div>
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Skeleton className="h-8 w-full rounded" />
          <Skeleton className="h-8 w-full rounded" />
        </div>
        <Skeleton className="h-3 w-full rounded-full" />
      </div>

      {/* Task table */}
      <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
        <div className="px-5 py-3 border-b border-gray-800">
          <Skeleton className="h-4 w-16" />
        </div>
        <div className="divide-y divide-gray-800">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex items-center gap-4 px-4 py-3">
              <Skeleton className="h-3 w-24" />
              <Skeleton className="h-5 w-16 rounded-full" />
              <Skeleton className="h-3 w-32" />
              <Skeleton className="h-3 w-16 ml-auto" />
              <Skeleton className="h-3 w-12" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

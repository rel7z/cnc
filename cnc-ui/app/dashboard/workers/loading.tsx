import { Skeleton } from "@/components/ui/Skeleton";

export default function WorkersLoading() {
  return (
    <div className="p-6">
      <div className="bg-gray-900 border border-gray-800 rounded-xl overflow-hidden">
        <div className="px-5 py-3 border-b border-gray-800">
          <Skeleton className="h-4 w-36" />
          <Skeleton className="h-3 w-52 mt-1.5" />
        </div>
        <div className="divide-y divide-gray-800">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="flex items-center gap-4 px-4 py-3">
              <Skeleton className="h-3 w-40" />
              <Skeleton className="h-3 w-28" />
              <Skeleton className="h-5 w-16 rounded-full" />
              <Skeleton className="h-2 w-24 rounded-full" />
              <Skeleton className="h-3 w-20 ml-auto" />
              <Skeleton className="h-3 w-16" />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

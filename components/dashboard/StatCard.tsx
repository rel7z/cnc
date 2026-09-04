"use client";

interface StatCardProps {
  label: string;
  value: number | null;
  color?: "default" | "green" | "blue" | "yellow" | "red";
  description?: string;
}

const valueColors: Record<NonNullable<StatCardProps["color"]>, string> = {
  default: "text-white",
  green: "text-emerald-400",
  blue: "text-blue-400",
  yellow: "text-yellow-400",
  red: "text-red-400",
};

export function StatCard({
  label,
  value,
  color = "default",
  description,
}: StatCardProps) {
  return (
    <div className="bg-gray-900 border border-gray-800 rounded-xl p-5 flex flex-col gap-1">
      <span className="text-xs font-medium text-gray-400 uppercase tracking-wider">
        {label}
      </span>
      <span
        className={`text-3xl font-bold tabular-nums transition-all duration-300 ${valueColors[color]}`}
      >
        {value === null ? (
          <span className="inline-block h-9 w-12 animate-pulse rounded bg-gray-800" />
        ) : (
          value
        )}
      </span>
      {description && (
        <span className="text-xs text-gray-500">{description}</span>
      )}
    </div>
  );
}

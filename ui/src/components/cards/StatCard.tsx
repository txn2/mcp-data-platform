import { cn } from "@/lib/utils";

interface StatCardProps {
  label: string;
  // Usually a formatted figure, but a stat that carries a verdict rather than a
  // magnitude (a success rate, a health state) renders its own badge here.
  value: React.ReactNode;
  detail?: string;
  className?: string;
}

export function StatCard({ label, value, detail, className }: StatCardProps) {
  return (
    <div
      className={cn(
        "rounded-lg border bg-card p-4 shadow-sm",
        className,
      )}
    >
      <p className="text-sm font-medium text-muted-foreground">{label}</p>
      <p className="mt-1 text-2xl font-bold">{value}</p>
      {detail && (
        <p className="mt-1 text-xs text-muted-foreground">{detail}</p>
      )}
    </div>
  );
}

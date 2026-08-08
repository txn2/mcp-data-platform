import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";

interface StatCardProps {
  label: string;
  // Usually a formatted figure, but a stat that carries a verdict rather than a
  // magnitude (a success rate, a health state) renders its own badge here.
  value: React.ReactNode;
  detail?: string;
  className?: string;
}

// StatCard is one figure in a row of them: the ui/card face at a tile's
// density, so a stat row and the sections under it are the same box.
export function StatCard({ label, value, detail, className }: StatCardProps) {
  return (
    <Card className={cn("gap-1 p-4", className)}>
      <p className="text-sm font-medium text-muted-foreground">{label}</p>
      <p className="text-2xl font-bold">{value}</p>
      {detail && <p className="text-xs text-muted-foreground">{detail}</p>}
    </Card>
  );
}

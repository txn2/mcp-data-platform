import { CheckCircle2 } from "lucide-react";
import { useSignoff } from "@/api/portal/hooks";
import { cn } from "@/lib/utils";

// SignoffBadge renders "signed off by N of M stakeholders" for an asset or
// collection (#603). Hidden until the summary loads.
export function SignoffBadge({
  targetType,
  id,
  enabled = true,
}: {
  targetType: "assets" | "collections";
  id: string;
  enabled?: boolean;
}) {
  const { data } = useSignoff(targetType, id, enabled);
  if (!data) return null;
  const complete = data.signed_off >= data.stakeholders;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs",
        complete
          ? "bg-emerald-50 text-emerald-900 dark:bg-emerald-500/10 dark:text-emerald-200"
          : "bg-muted text-muted-foreground",
      )}
      title="Distinct stakeholders who approved out of owner + active share grantees"
    >
      <CheckCircle2 className="h-3 w-3" /> Signed off by {data.signed_off} of {data.stakeholders}
    </span>
  );
}

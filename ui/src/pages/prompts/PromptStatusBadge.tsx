import { Badge } from "@/components/ui/badge";

const STATUS_VARIANTS: Record<string, "muted" | "success" | "warning" | "danger"> = {
  draft: "muted",
  approved: "success",
  deprecated: "warning",
  superseded: "danger",
};

// PromptStatusBadge renders a prompt's lifecycle status as a colored pill.
export function PromptStatusBadge({ status }: { status?: string }) {
  if (!status) return null;
  return (
    <Badge
      variant={STATUS_VARIANTS[status] ?? STATUS_VARIANTS.draft}
      className="text-[11px] capitalize"
    >
      {status}
    </Badge>
  );
}

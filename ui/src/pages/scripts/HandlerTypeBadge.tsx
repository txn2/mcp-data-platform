import { Badge } from "@/components/ui/badge";
import { HANDLER_TYPE_LABELS, type HandlerType } from "@/api/portal/hooks/scriptHandlers";
import { cn } from "@/lib/utils";

// HandlerTypeBadge names what starts a script (#2435, #2436): Endpoint, Form,
// Schedule, Webhook, Policy, Transform or Watch, the words the Type filter
// offers. The list rows, the
// script's Handlers section and Admin > Handlers all draw it from here, so
// the three surfaces cannot name a type differently.
const TYPE_VARIANTS: Record<HandlerType, "info" | "success" | "warning" | "secondary" | "danger" | "muted"> = {
  endpoint: "info",
  form: "success",
  schedule: "secondary",
  webhook: "warning",
  policy: "danger",
  transform: "muted",
  watch: "secondary",
};

export function HandlerTypeBadge({ type, className }: { type: HandlerType; className?: string }) {
  return (
    <Badge variant={TYPE_VARIANTS[type] ?? "secondary"} className={className}>
      {HANDLER_TYPE_LABELS[type] ?? type}
    </Badge>
  );
}

// HandlerTypeBadges is a script's handler types, once each; nothing for a
// script with none. omit leaves out a type the caller shows another way, as a
// list row showing its endpoint's address does.
export function HandlerTypeBadges({
  types,
  omit = [],
  className,
}: {
  types?: string[];
  omit?: HandlerType[];
  className?: string;
}) {
  const shown = (types ?? []).filter((t): t is HandlerType => t in HANDLER_TYPE_LABELS && !omit.includes(t as HandlerType));
  if (shown.length === 0) return null;
  return (
    <span className={cn("inline-flex flex-wrap items-center gap-1", className)} data-testid="handler-types">
      {shown.map((t) => (
        <HandlerTypeBadge key={t} type={t} />
      ))}
    </span>
  );
}

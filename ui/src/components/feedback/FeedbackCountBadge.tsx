import { MessageCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

interface Props {
  count: number | undefined;
  className?: string;
}

// FeedbackCountBadge renders an open-thread count pill for a list card, or
// nothing when there are no open threads.
export function FeedbackCountBadge({ count, className }: Props) {
  if (!count || count <= 0) return null;
  return (
    <Badge
      variant="info"
      title={`${count} open feedback ${count === 1 ? "thread" : "threads"}`}
      className={cn("px-1.5 text-[11px]", className)}
    >
      <MessageCircle />
      {count}
    </Badge>
  );
}

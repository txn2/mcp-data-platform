import { MessageCircle } from "lucide-react";

interface Props {
  count: number | undefined;
  className?: string;
}

// FeedbackCountBadge renders an open-thread count pill for a list card, or
// nothing when there are no open threads.
export function FeedbackCountBadge({ count, className = "" }: Props) {
  if (!count || count <= 0) return null;
  return (
    <span
      title={`${count} open feedback ${count === 1 ? "thread" : "threads"}`}
      className={`inline-flex items-center gap-1 rounded-full bg-blue-100 px-1.5 py-0.5 text-[11px] font-medium text-blue-700 dark:bg-blue-950 dark:text-blue-300 ${className}`}
    >
      <MessageCircle className="h-3 w-3" />
      {count}
    </span>
  );
}

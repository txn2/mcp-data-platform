import { useRunPosts, type RunPost } from "@/api/portal/hooks/channels";
import { StatusBadge } from "@/components/cards/StatusBadge";

// STATUS_VARIANT tints a post's delivery: delivered, given up on, or waiting.
const STATUS_VARIANT: Record<RunPost["status"], "success" | "error" | "neutral"> = {
  sent: "success",
  failed: "error",
  queued: "neutral",
};

// ScriptRunPosts lists what a run posted to notification channels with
// platform.notify and platform.publish (#1723), and whether each arrived. A
// run that posted nothing shows nothing: most runs post nothing, and a line
// saying so under every run would be noise.
export function ScriptRunPosts({ scriptId, runId }: { scriptId: string; runId: string }) {
  const { data } = useRunPosts(scriptId, runId);
  const posts = data?.posts ?? [];
  if (posts.length === 0) return null;
  return (
    <ul className="space-y-1 text-xs" data-testid="run-posts">
      {posts.map((p) => (
        <li key={`${p.channel}-${p.queued_at}`} className="flex flex-wrap items-center gap-2">
          <span className="font-medium">Posted to {p.channel}</span>
          <span className="text-muted-foreground">{p.title}</span>
          <StatusBadge variant={STATUS_VARIANT[p.status] ?? "neutral"}>{p.status}</StatusBadge>
          {p.deliveries > 1 && (
            <span className="text-muted-foreground">
              {p.sent} of {p.deliveries} delivered
            </span>
          )}
          {p.last_error && (
            <span className="basis-full truncate text-destructive" title={p.last_error}>
              {p.last_error}
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

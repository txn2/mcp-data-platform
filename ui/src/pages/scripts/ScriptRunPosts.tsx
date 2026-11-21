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
          {(p.opted_out ?? 0) > 0 && (
            <span className="text-amber-700 dark:text-amber-400" data-testid="run-post-opted-out">
              {optedOutText(p.opted_out ?? 0, p.deliveries)}
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

// optedOutText says how many of a list a post did not reach because they
// turned email notifications off (#2743): a feed that stopped reaching
// someone shows on the run that sent it.
export function optedOutText(optedOut: number, deliveries: number): string {
  const total = optedOut + deliveries;
  return `${optedOut} of ${total} recipient${total === 1 ? "" : "s"} opted out of email`;
}

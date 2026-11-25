import type { BreakdownEntry } from "@/api/admin/types";
import { ChartSkeleton } from "@/components/charts/ChartSkeleton";

// What each event kind other than a tool call is called on the page. A kind
// the platform adds later shows under its own name until it is named here.
const eventKindNames: Record<string, string> = {
  script_run: "Scheduled and requested script runs",
  resource_read: "Managed resources read",
  resource_move: "Managed resources moved",
  prompt_serve: "Prompts served",
  endpoint_request: "Endpoint requests",
  apigateway_invoke: "API gateway calls",
  form_decrypt: "Form submissions decrypted",
  admin: "Admin actions",
};

// OtherEventsPanel lists the events recorded beside tool calls (#2819), which
// are no longer counted under a tool's name.
export function OtherEventsPanel({
  data,
  isLoading,
  onOpenEvents,
}: {
  data: BreakdownEntry[] | undefined;
  isLoading: boolean;
  onOpenEvents?: () => void;
}) {
  if (isLoading || !data) return <ChartSkeleton height={140} />;
  const others = data.filter((d) => d.dimension !== "mcp_tool_call" && d.dimension !== "");
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        Recorded beside tool calls and not counted under a tool's name.
      </p>
      {others.length === 0 ? (
        <p className="text-sm text-muted-foreground">No other events in this range.</p>
      ) : (
        <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-2 text-sm">
          {others.map((d) => (
            <div key={d.dimension} className="contents">
              <dt>{eventKindNames[d.dimension] ?? d.dimension}</dt>
              <dd className="text-right font-semibold tabular-nums">{d.count.toLocaleString()}</dd>
            </div>
          ))}
        </dl>
      )}
      <p className="rounded-md border bg-muted/40 p-2 text-xs text-muted-foreground">
        Rows written before this release keep the counts they had. Script calls recorded before it
        may show as agent calls.
      </p>
      {onOpenEvents && (
        <button type="button" className="text-sm text-primary hover:underline" onClick={onOpenEvents}>
          Open the Events tab
        </button>
      )}
    </div>
  );
}

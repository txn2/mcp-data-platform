import type { ThreadEvent, ThreadStatus } from "@/api/portal/types";
import { MentionText } from "../MentionText";
import { STATUS_LABEL, formatRelative } from "../meta";

// What each non-comment timeline event says it did. Statuses and ratings carry
// a value, so they are derived below; everything else is a fixed phrase.
const EVENT_SUMMARY: Record<string, string> = {
  approval: "approved",
  rejection: "rejected",
  validation_request: "requested validation",
  validation_result: "recorded a validation result",
  insight_linked: "linked an insight",
  changeset_linked: "linked a changeset",
};

// eventSummary renders a one-line label for non-comment timeline events.
function eventSummary(e: ThreadEvent): string | null {
  if (e.event_type === "status_change" || e.event_type === "resolution") {
    const next = (e.metadata?.["new_status"] as string) ?? "";
    if (!next) return "changed status";
    return `changed status to ${STATUS_LABEL[next as ThreadStatus] ?? next}`;
  }
  if (e.event_type === "rating") {
    return e.rating == null ? "left a rating" : `rated ${e.rating}/5`;
  }
  return EVENT_SUMMARY[e.event_type] ?? null;
}

// recordedMentions returns the addresses the server recorded as delivered
// mentions for an event. Anything else in the body was not delivered and must
// not render as a chip.
function recordedMentions(e: ThreadEvent): string[] {
  const raw = e.metadata?.["mentions"];
  return Array.isArray(raw) ? raw.filter((m): m is string => typeof m === "string") : [];
}

// ThreadTimeline is the thread's record: who did what, when, and the comment
// bodies with their delivered mentions resolved to names.
export function ThreadTimeline({
  events,
  isLoading,
}: {
  events: ThreadEvent[] | undefined;
  isLoading: boolean;
}) {
  return (
    <div className="flex-1 space-y-3 overflow-auto p-3">
      {isLoading && <p className="text-xs text-muted-foreground">Loading timeline…</p>}
      {(events ?? []).map((e) => (
        <div key={e.id} className="text-sm">
          <p className="text-xs text-muted-foreground">
            <span className="font-medium text-foreground">{e.author_email}</span>{" "}
            {eventSummary(e) ?? "commented"} · {formatRelative(e.created_at)}
          </p>
          {e.body && (
            <MentionText
              body={e.body}
              mentions={recordedMentions(e)}
              className="mt-0.5 whitespace-pre-wrap"
            />
          )}
        </div>
      ))}
    </div>
  );
}

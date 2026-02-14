import type { AuditEvent } from "@/api/types";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { formatDuration } from "@/lib/formatDuration";
import { formatUser } from "@/lib/formatUser";

export function EventDrawer({
  event,
  onClose,
}: {
  event: AuditEvent;
  onClose: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div
        className="absolute inset-0 bg-black/50"
        onClick={onClose}
      />
      <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Event Detail</h2>
          <button
            onClick={onClose}
            className="rounded-md px-2 py-1 text-sm hover:bg-muted"
          >
            Close
          </button>
        </div>

        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <p className="text-xs text-muted-foreground">Event ID</p>
              <p className="font-mono text-xs">{event.id}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Timestamp</p>
              <p>{new Date(event.timestamp).toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">User</p>
              <p title={event.user_id}>
                {formatUser(event.user_id, event.user_email)}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Persona</p>
              <p>{event.persona || "-"}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Tool</p>
              <p className="font-mono text-xs">{event.tool_name}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Toolkit</p>
              <p>
                {event.toolkit_kind} / {event.toolkit_name}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Connection</p>
              <p>{event.connection}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Duration</p>
              <p>{formatDuration(event.duration_ms)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Status</p>
              <StatusBadge
                variant={event.success ? "success" : "error"}
              >
                {event.success ? "Success" : "Error"}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Enriched</p>
              <StatusBadge
                variant={
                  event.enrichment_applied ? "success" : "neutral"
                }
              >
                {event.enrichment_applied ? "Yes" : "No"}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Transport</p>
              <p>{event.transport}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Session</p>
              <p className="font-mono text-xs">{event.session_id}</p>
            </div>
          </div>

          <div className="grid grid-cols-3 gap-3 text-sm">
            <div>
              <p className="text-xs text-muted-foreground">Request Chars</p>
              <p>{event.request_chars.toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Response Chars</p>
              <p>{event.response_chars.toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Content Blocks</p>
              <p>{event.content_blocks}</p>
            </div>
          </div>

          {event.error_message && (
            <div>
              <p className="text-xs text-muted-foreground">Error Message</p>
              <p className="mt-1 rounded bg-red-50 p-2 text-sm text-red-800 break-words">
                {event.error_message}
              </p>
            </div>
          )}

          {event.parameters &&
            Object.keys(event.parameters).length > 0 && (
              <div>
                <p className="mb-1 text-xs text-muted-foreground">
                  Parameters
                </p>
                <pre className="overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-3 text-xs">
                  {JSON.stringify(event.parameters, null, 2)}
                </pre>
              </div>
            )}
        </div>
      </div>
    </div>
  );
}

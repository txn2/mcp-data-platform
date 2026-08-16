import type { AuditEvent } from "@/api/admin/types";
import { useToolSchemas } from "@/api/admin/hooks";
import { useInspectorStore } from "@/stores/inspector";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { DrawerShell } from "@/components/patterns/DrawerShell";
import { formatDuration } from "@/lib/formatDuration";
import { formatUser } from "@/lib/formatUser";
import { Play } from "lucide-react";

export function EventDrawer({
  event,
  onClose,
  onNavigate,
}: {
  event: AuditEvent;
  onClose: () => void;
  onNavigate?: (path: string) => void;
}) {
  const { data: schemasData } = useToolSchemas();
  const setReplayIntent = useInspectorStore((s) => s.setReplayIntent);

  const schemas = schemasData?.schemas ?? {};
  const toolExists = event.tool_name in schemas;

  const handleReplay = () => {
    setReplayIntent({
      tool_name: event.tool_name,
      connection: event.connection ?? "",
      parameters: event.parameters ?? {},
      event_id: event.id,
      event_timestamp: event.timestamp,
    });
    // Navigate to the search-param URL the Tools page actually reads
    // (ToolsPage.readSelectionFromURL keys on ?selected=<tool>&tab=<tab>).
    // This selects the event's tool and opens the Try It tab so the mounted
    // ToolDetail matches the stashed replay intent and pre-fills the form.
    // The legacy "#explore" hash set no search params, so the page fell back
    // to the first tool on the Overview tab and the intent was never consumed.
    onNavigate?.(
      `/admin/tools?selected=${encodeURIComponent(event.tool_name)}&tab=tryit`,
    );
  };

  const replay = onNavigate ? (
    <div>
      <Button
        type="button"
        onClick={handleReplay}
        disabled={!toolExists}
        title={
          toolExists
            ? "Open this tool call in the Inspector with parameters pre-filled"
            : `Tool "${event.tool_name}" is not in the current catalog`
        }
      >
        <Play />
        Replay in Inspector
      </Button>
      {!toolExists && (
        <p className="mt-1.5 text-xs text-muted-foreground">
          Tool not found in current catalog
        </p>
      )}
    </div>
  ) : undefined;

  return (
    <DrawerShell title="Event Detail" onClose={onClose} footer={replay}>
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
          <StatusBadge variant={event.success ? "success" : "error"}>
            {event.success ? "Success" : "Error"}
          </StatusBadge>
        </div>
        <div>
          <p className="text-xs text-muted-foreground">Enriched</p>
          <StatusBadge
            variant={event.enrichment_applied ? "success" : "neutral"}
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
          <Alert variant="destructive" className="mt-1">
            <AlertDescription className="break-words">
              {event.error_message}
            </AlertDescription>
          </Alert>
        </div>
      )}

      {event.purpose && (
        <div>
          <p className="mb-1 text-xs text-muted-foreground">Purpose</p>
          <p className="whitespace-pre-wrap break-words rounded bg-muted p-3 text-sm">
            {event.purpose}
          </p>
        </div>
      )}

      {event.parameters && Object.keys(event.parameters).length > 0 && (
        <div>
          <p className="mb-1 text-xs text-muted-foreground">Parameters</p>
          <pre className="overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-3 text-xs">
            {JSON.stringify(event.parameters, null, 2)}
          </pre>
        </div>
      )}
    </DrawerShell>
  );
}

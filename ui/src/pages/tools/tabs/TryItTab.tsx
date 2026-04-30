import { useCallTool, useToolSchemas } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { formatDuration } from "@/lib/formatDuration";
import { ToolForm } from "../ToolForm";
import { ToolResult } from "../ToolResult";
import type { ToolCallResponse, ToolDetail } from "@/api/admin/types";
import type { HistoryEntry, TryItSession } from "../useTryItSession";
import { X } from "lucide-react";

export function TryItTab({
  detail,
  session,
}: {
  detail: ToolDetail;
  session: TryItSession;
}) {
  const { data: schemasData } = useToolSchemas();
  const callTool = useCallTool();

  const {
    history,
    latestResult,
    showRaw,
    historyOpen,
    replayParams,
    replaySource,
    formVersion,
    addHistoryEntry,
    updateHistoryEntry,
    clearHistory,
    setLatestResult,
    toggleRaw,
    toggleHistory,
    applyReplay,
    dismissReplay,
  } = session;

  const schema = schemasData?.schemas[detail.name] ?? null;
  const connection = detail.connection ?? "";

  function handleSubmit(params: Record<string, unknown>) {
    if (!schema) return;
    const entryId = `call-${Date.now()}`;
    const entry: HistoryEntry = {
      id: entryId,
      timestamp: new Date().toISOString(),
      parameters: params,
      response: null,
      is_loading: true,
    };
    addHistoryEntry(entry);
    setLatestResult(null);

    const properties = schema.parameters.properties ?? {};
    const sendConnection = "connection" in properties ? connection : "";

    callTool.mutate(
      {
        tool_name: detail.name,
        connection: sendConnection,
        parameters: params,
      },
      {
        onSuccess: (data) => {
          setLatestResult(data);
          updateHistoryEntry(entryId, { response: data, is_loading: false });
        },
        onError: () => {
          const errorResp: ToolCallResponse = {
            content: [{ type: "text", text: "Request failed" }],
            is_error: true,
            duration_ms: 0,
          };
          setLatestResult(errorResp);
          updateHistoryEntry(entryId, { response: errorResp, is_loading: false });
        },
      },
    );
  }

  function handleReplay(entry: HistoryEntry) {
    applyReplay({ params: entry.parameters, source: null });
  }

  if (!schema) {
    return (
      <div className="text-sm text-muted-foreground">
        No input schema is registered for this tool, so it can&apos;t be invoked
        from the portal.
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {replaySource && (
        <div className="flex items-center gap-3 rounded-lg border border-primary/20 bg-primary/5 px-4 py-2.5 text-sm">
          <span className="flex-1 text-muted-foreground">
            Replaying audit event{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
              {replaySource.event_id.slice(0, 8)}
            </code>{" "}
            from {new Date(replaySource.event_timestamp).toLocaleString()}
          </span>
          <button
            type="button"
            onClick={dismissReplay}
            className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
            title="Dismiss"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      <ToolForm
        schema={schema}
        selectedConnection={connection}
        initialValues={replayParams ?? undefined}
        isSubmitting={callTool.isPending}
        onSubmit={handleSubmit}
        formVersion={formVersion}
      />

      {latestResult && (
        <ToolResult
          result={latestResult}
          toolKind={schema.kind}
          showRaw={showRaw}
          onToggleRaw={toggleRaw}
        />
      )}

      <div className="rounded-lg border bg-card">
        <button
          onClick={toggleHistory}
          className="flex w-full items-center justify-between border-b p-3 text-sm font-medium"
        >
          <span>
            History{" "}
            <span className="text-muted-foreground">({history.length})</span>
          </span>
          <span className="text-xs text-muted-foreground">
            {historyOpen ? "Collapse" : "Expand"}
          </span>
        </button>
        {historyOpen && (
          <div className="overflow-auto">
            {history.length === 0 ? (
              <p className="px-3 py-6 text-center text-sm text-muted-foreground">
                No test calls yet.
              </p>
            ) : (
              <>
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-3 py-2 text-left font-medium">Time</th>
                      <th className="px-3 py-2 text-right font-medium">
                        Duration
                      </th>
                      <th className="px-3 py-2 text-center font-medium">
                        Status
                      </th>
                      <th className="px-3 py-2 text-center font-medium">
                        Action
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {history.map((entry) => (
                      <tr key={entry.id} className="border-b">
                        <td className="px-3 py-2 text-xs">
                          {new Date(entry.timestamp).toLocaleTimeString()}
                        </td>
                        <td className="px-3 py-2 text-right text-xs">
                          {entry.is_loading
                            ? "…"
                            : entry.response
                              ? formatDuration(entry.response.duration_ms)
                              : "-"}
                        </td>
                        <td className="px-3 py-2 text-center">
                          {entry.is_loading ? (
                            <StatusBadge variant="warning">Running</StatusBadge>
                          ) : entry.response?.is_error ? (
                            <StatusBadge variant="error">Error</StatusBadge>
                          ) : (
                            <StatusBadge variant="success">Success</StatusBadge>
                          )}
                        </td>
                        <td className="px-3 py-2 text-center">
                          <button
                            onClick={() => handleReplay(entry)}
                            className="rounded px-2 py-0.5 text-xs text-primary hover:bg-primary/10"
                          >
                            Replay
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <div className="flex justify-end border-t p-2">
                  <button
                    onClick={clearHistory}
                    className="rounded px-2 py-1 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
                  >
                    Clear
                  </button>
                </div>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

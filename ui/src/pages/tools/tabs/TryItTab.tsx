import { useEffect, useRef, useState } from "react";
import { useCallTool, useToolSchemas } from "@/api/admin/hooks";
import { useInspectorStore } from "@/stores/inspector";
import type { ReplayIntent } from "@/stores/inspector";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { formatDuration } from "@/lib/formatDuration";
import { ToolForm } from "../ToolForm";
import { ToolResult } from "../ToolResult";
import type { ToolCallResponse, ToolDetail } from "@/api/admin/types";
import { X } from "lucide-react";

interface HistoryEntry {
  id: string;
  timestamp: string;
  parameters: Record<string, unknown>;
  response: ToolCallResponse | null;
  is_loading: boolean;
}

export function TryItTab({ detail }: { detail: ToolDetail }) {
  const { data: schemasData } = useToolSchemas();
  const callTool = useCallTool();
  // Peek at the intent — only consume when the tool matches the current
  // selection so we don't drop someone else's pending replay.
  const replayIntent = useInspectorStore((s) => s.replayIntent);
  const consumeReplayIntent = useInspectorStore((s) => s.consumeReplayIntent);

  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [latestResult, setLatestResult] = useState<ToolCallResponse | null>(null);
  const [showRaw, setShowRaw] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(true);
  const [replayParams, setReplayParams] = useState<
    Record<string, unknown> | null
  >(null);
  const [replaySource, setReplaySource] = useState<{
    event_id: string;
    event_timestamp: string;
  } | null>(null);
  const [formVersion, setFormVersion] = useState(0);

  const schema = schemasData?.schemas[detail.name] ?? null;
  const connection = detail.connection ?? "";

  // Reset session state when the selected tool changes.
  useEffect(() => {
    setHistory([]);
    setLatestResult(null);
    setShowRaw(false);
    setReplayParams(null);
    setReplaySource(null);
    setFormVersion((v) => v + 1);
  }, [detail.name]);

  // Consume a replay intent from the inspector store (set by EventDrawer).
  // Only fires when the requested tool matches the currently-selected one;
  // intents for other tools stay in the store for the matching mount.
  const consumedRef = useRef<string | null>(null);
  useEffect(() => {
    if (consumedRef.current === detail.name) return;
    const intent: ReplayIntent | null = replayIntent;
    if (!intent || intent.tool_name !== detail.name) return;
    consumeReplayIntent();
    consumedRef.current = detail.name;
    setReplayParams(intent.parameters);
    setReplaySource({
      event_id: intent.event_id,
      event_timestamp: intent.event_timestamp,
    });
    setLatestResult(null);
    setShowRaw(false);
    setFormVersion((v) => v + 1);
  }, [replayIntent, consumeReplayIntent, detail.name]);

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
    setHistory((prev) => [entry, ...prev]);
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
          setHistory((prev) =>
            prev.map((h) =>
              h.id === entryId
                ? { ...h, response: data, is_loading: false }
                : h,
            ),
          );
        },
        onError: () => {
          const errorResp: ToolCallResponse = {
            content: [{ type: "text", text: "Request failed" }],
            is_error: true,
            duration_ms: 0,
          };
          setLatestResult(errorResp);
          setHistory((prev) =>
            prev.map((h) =>
              h.id === entryId
                ? { ...h, response: errorResp, is_loading: false }
                : h,
            ),
          );
        },
      },
    );
  }

  function handleReplay(entry: HistoryEntry) {
    setReplayParams(entry.parameters);
    setReplaySource(null);
    setLatestResult(null);
    setShowRaw(false);
    setFormVersion((v) => v + 1);
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
            onClick={() => {
              setReplaySource(null);
              setReplayParams(null);
            }}
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
          onToggleRaw={() => setShowRaw((v) => !v)}
        />
      )}

      <div className="rounded-lg border bg-card">
        <button
          onClick={() => setHistoryOpen((v) => !v)}
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
                    onClick={() => setHistory([])}
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

import { useMemo } from "react";
import {
  useCallTool,
  useEffectiveConnections,
  useToolSchemas,
} from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
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

  // Platform-level tools (no bound connection) need an operator-
  // selectable picker filtered to the tool's kind. The hook returns
  // every connection regardless of source (file or database) so the
  // picker matches the connection-list shown in Settings.
  const { data: allConnections } = useEffectiveConnections();
  const availableConnections = useMemo(() => {
    if (!schema || connection) return undefined;
    const targetKind = schema.kind;
    if (!targetKind) return undefined;
    return (allConnections ?? []).filter((c) => c.kind === targetKind);
  }, [allConnections, schema, connection]);

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
    // Routing connection: bound at toolkit registration (locked
    // select) takes precedence; otherwise the operator's pick from
    // the enabled dropdown (which arrives in params.connection
    // because the new picker uses name="connection"). Build a
    // separate outParams via destructuring so the history entry's
    // captured params (referenced from the History panel and the
    // Replay action) keeps the connection field for audit/replay.
    let sendConnection = "";
    let outParams: Record<string, unknown> = params;
    if ("connection" in properties) {
      if (connection) {
        sendConnection = connection;
      } else if (typeof params.connection === "string") {
        sendConnection = params.connection;
      }
      const { connection: _routing, ...rest } = params;
      void _routing;
      outParams = rest;
    }

    callTool.mutate(
      {
        tool_name: detail.name,
        connection: sendConnection,
        parameters: outParams,
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
        <Alert>
          <AlertDescription className="flex w-full items-center gap-3 text-sm">
            <span className="flex-1">
              Replaying audit event{" "}
              <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">
                {replaySource.event_id.slice(0, 8)}
              </code>{" "}
              from {new Date(replaySource.event_timestamp).toLocaleString()}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={dismissReplay}
              title="Dismiss"
              aria-label="Dismiss replay"
            >
              <X />
            </Button>
          </AlertDescription>
        </Alert>
      )}

      <ToolForm
        schema={schema}
        selectedConnection={connection}
        availableConnections={availableConnections}
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

      <CallHistory
        history={history}
        open={historyOpen}
        onToggle={toggleHistory}
        onClear={clearHistory}
        onReplay={(entry) => applyReplay({ params: entry.parameters, source: null })}
      />
    </div>
  );
}

// CallHistory is the session's record of what has been invoked from the portal,
// with a replay per entry so a call can be re-run with the same parameters.
function CallHistory({
  history,
  open,
  onToggle,
  onClear,
  onReplay,
}: {
  history: HistoryEntry[];
  open: boolean;
  onToggle: () => void;
  onClear: () => void;
  onReplay: (entry: HistoryEntry) => void;
}) {
  return (
    <div className="rounded-lg border bg-card">
      <Button
        variant="ghost"
        onClick={onToggle}
        aria-expanded={open}
        className="w-full justify-between rounded-b-none border-b p-3 text-sm font-medium"
      >
        <span>
          History <span className="text-muted-foreground">({history.length})</span>
        </span>
        <span className="text-xs font-normal text-muted-foreground">
          {open ? "Collapse" : "Expand"}
        </span>
      </Button>
      {open && (
        <div className="overflow-auto">
          {history.length === 0 ? (
            <p className="px-3 py-6 text-center text-sm text-muted-foreground">
              No test calls yet.
            </p>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow className="bg-muted/50 hover:bg-muted/50">
                    <TableHead className="px-3">Time</TableHead>
                    <TableHead className="px-3 text-right">Duration</TableHead>
                    <TableHead className="px-3 text-center">Status</TableHead>
                    <TableHead className="px-3 text-center">Action</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.map((entry) => (
                    <TableRow key={entry.id}>
                      <TableCell className="px-3 text-xs">
                        {new Date(entry.timestamp).toLocaleTimeString()}
                      </TableCell>
                      <TableCell className="px-3 text-right text-xs">
                        {entry.is_loading
                          ? "…"
                          : entry.response
                            ? formatDuration(entry.response.duration_ms)
                            : "-"}
                      </TableCell>
                      <TableCell className="px-3 text-center">
                        {entry.is_loading ? (
                          <StatusBadge variant="warning">Running</StatusBadge>
                        ) : entry.response?.is_error ? (
                          <StatusBadge variant="error">Error</StatusBadge>
                        ) : (
                          <StatusBadge variant="success">Success</StatusBadge>
                        )}
                      </TableCell>
                      <TableCell className="px-3 text-center">
                        <Button variant="link" size="xs" onClick={() => onReplay(entry)}>
                          Replay
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              <div className="flex justify-end border-t p-2">
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={onClear}
                  className="text-muted-foreground"
                >
                  Clear
                </Button>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

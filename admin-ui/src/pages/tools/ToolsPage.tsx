import { useState, useMemo, useCallback, useEffect, useRef } from "react";
import {
  useTools,
  useConnections,
  useToolSchemas,
  useCallTool,
} from "@/api/hooks";
import type { ToolSchema } from "@/api/types";
import type {
  ToolCallResponse,
  ConnectionInfo,
} from "@/api/types";
import { useInspectorStore } from "@/stores/inspector";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { formatDuration } from "@/lib/formatDuration";
import { Eye, EyeOff, X } from "lucide-react";

type Tab = "overview" | "explore" | "help";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "explore", label: "Explore" },
  { key: "help", label: "Help" },
];

export function ToolsPage({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(
    (["overview", "explore", "help"].includes(initialTab ?? "") ? initialTab : "overview") as Tab,
  );

  return (
    <div className="space-y-4">
      <div className="flex gap-1 border-b">
        {TAB_ITEMS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === "overview" && <OverviewTab />}
      {tab === "explore" && <ExploreTab />}
      {tab === "help" && <ToolsHelpTab />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview Tab
// ---------------------------------------------------------------------------

function OverviewTab() {
  const { data: toolsData } = useTools();
  const { data: connectionsData } = useConnections();
  const { data: schemasData } = useToolSchemas();

  const tools = toolsData?.tools ?? [];
  const connections = connectionsData?.connections ?? [];
  const schemas: Record<string, ToolSchema> = schemasData?.schemas ?? {};

  const hiddenToolSet = useMemo(
    () => new Set(connections.flatMap((c) => c.hidden_tools ?? [])),
    [connections],
  );

  return (
    <div className="space-y-6">
      {/* Connections grid */}
      {connections.length > 0 && (
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Connections</h2>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {connections.map((c) => (
              <div key={c.name} className="rounded-md border p-3">
                <div className="flex items-center gap-2">
                  <StatusBadge variant="success">{c.kind}</StatusBadge>
                  <span className="text-sm font-medium">{c.name}</span>
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {c.tools.length} tools
                </p>
                <div className="mt-2 flex flex-wrap gap-1">
                  {c.tools.map((toolName) => (
                    <span
                      key={toolName}
                      className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground"
                    >
                      {toolName}
                    </span>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Tool inventory table */}
      <div className="rounded-lg border bg-card">
        <div className="border-b p-4">
          <h2 className="text-sm font-medium">Tool Inventory</h2>
        </div>
        <div className="overflow-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Name</th>
                <th className="px-3 py-2 text-left font-medium">Kind</th>
                <th className="px-3 py-2 text-left font-medium">Connection</th>
                <th className="px-3 py-2 text-left font-medium">Toolkit</th>
              </tr>
            </thead>
            <tbody>
              {tools.map((tool, idx) => {
                const isHidden = hiddenToolSet.has(tool.name);
                const desc = schemas[tool.name]?.description;
                return (
                  <tr key={`${tool.name}-${tool.connection}-${idx}`} className="border-b">
                    <td className="px-3 py-2">
                      <span className="flex items-center gap-1.5 font-mono text-xs">
                        {isHidden ? (
                          <EyeOff className="h-3 w-3 shrink-0 opacity-40" />
                        ) : (
                          <Eye className="h-3 w-3 shrink-0 opacity-40" />
                        )}
                        <span className={isHidden ? "opacity-50" : ""}>{tool.name}</span>
                      </span>
                      {desc && (
                        <p className="mt-0.5 pl-[18px] text-[11px] leading-snug text-muted-foreground">
                          {desc}
                        </p>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <StatusBadge variant="neutral">{tool.kind}</StatusBadge>
                    </td>
                    <td className="px-3 py-2 text-xs">{tool.connection}</td>
                    <td className="px-3 py-2 text-xs">{tool.toolkit}</td>
                  </tr>
                );
              })}
              {tools.length === 0 && (
                <tr>
                  <td
                    colSpan={4}
                    className="px-3 py-8 text-center text-muted-foreground"
                  >
                    No tools loaded
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Test Tab
// ---------------------------------------------------------------------------

interface HistoryEntry {
  id: string;
  timestamp: string;
  tool_name: string;
  connection: string;
  parameters: Record<string, unknown>;
  response: ToolCallResponse | null;
  is_loading: boolean;
}

function ExploreTab() {
  const { data: connectionsData } = useConnections();
  const { data: schemasData } = useToolSchemas();
  const callTool = useCallTool();

  const connections = connectionsData?.connections ?? [];
  const schemas = schemasData?.schemas ?? {};

  const consumeReplayIntent = useInspectorStore((s) => s.consumeReplayIntent);

  const [selectedTool, setSelectedTool] = useState<string | null>(null);
  const [selectedConnection, setSelectedConnection] = useState("");
  const [history, setHistory] = useState<HistoryEntry[]>([]);
  const [search, setSearch] = useState("");
  const [showRaw, setShowRaw] = useState(false);
  const [latestResult, setLatestResult] = useState<ToolCallResponse | null>(
    null,
  );
  const [historyOpen, setHistoryOpen] = useState(true);
  const [replayParams, setReplayParams] = useState<Record<string, unknown> | null>(null);
  const [replaySource, setReplaySource] = useState<{ event_id: string; event_timestamp: string } | null>(null);
  const [formVersion, setFormVersion] = useState(0);

  const schema = selectedTool ? schemas[selectedTool] ?? null : null;

  // Consume a replay intent from the inspector store (set by EventDrawer)
  const consumedRef = useRef(false);
  useEffect(() => {
    if (consumedRef.current) return;
    const intent = consumeReplayIntent();
    if (!intent) return;
    consumedRef.current = true;
    setSelectedTool(intent.tool_name);
    setSelectedConnection(intent.connection);
    setReplayParams(intent.parameters);
    setReplaySource({ event_id: intent.event_id, event_timestamp: intent.event_timestamp });
    setLatestResult(null);
    setShowRaw(false);
    setFormVersion((v) => v + 1);
  }, [consumeReplayIntent]);

  // Group connections for selector, filtered by search
  const filteredConnections = useMemo(() => {
    const lowerSearch = search.toLowerCase();
    return connections
      .map((c) => ({
        ...c,
        tools: c.tools.filter((t) => t.toLowerCase().includes(lowerSearch)),
      }))
      .filter((c) => c.tools.length > 0);
  }, [connections, search]);

  // Tools that have schemas but aren't in any connection (e.g. platform_info)
  const platformTools = useMemo(() => {
    const connTools = new Set(connections.flatMap((c) => c.tools));
    const lowerSearch = search.toLowerCase();
    return Object.keys(schemas)
      .filter((t) => !connTools.has(t) && t.toLowerCase().includes(lowerSearch))
      .sort();
  }, [connections, schemas, search]);

  // Set of tool names hidden by global visibility filter
  const hiddenToolSet = useMemo(
    () => new Set(connections.flatMap((c) => c.hidden_tools ?? [])),
    [connections],
  );

  const selectTool = useCallback(
    (toolName: string, connection: ConnectionInfo | null) => {
      setSelectedTool(toolName);
      setSelectedConnection(connection?.connection ?? "");
      setLatestResult(null);
      setShowRaw(false);
      setReplayParams(null);
      setReplaySource(null);
    },
    [],
  );

  const handleExecute = useCallback(
    (e: React.FormEvent<HTMLFormElement>) => {
      e.preventDefault();
      if (!selectedTool || !schema) return;

      const formData = new FormData(e.currentTarget);
      const params: Record<string, unknown> = {};
      const properties = schema.parameters.properties ?? {};
      const required = schema.parameters.required ?? [];
      for (const [key, propSchema] of Object.entries(properties)) {
        const val = formData.get(key);
        if (val === null || val === "") continue;
        if (propSchema.type === "integer") {
          params[key] = parseInt(String(val), 10);
        } else if (propSchema.type === "boolean") {
          params[key] = val === "on";
        } else {
          params[key] = String(val);
        }
      }

      // Check required
      for (const req of required) {
        if (params[req] === undefined || params[req] === "") return;
      }

      const entryId = `call-${Date.now()}`;
      const entry: HistoryEntry = {
        id: entryId,
        timestamp: new Date().toISOString(),
        tool_name: selectedTool,
        connection: selectedConnection,
        parameters: params,
        response: null,
        is_loading: true,
      };

      setHistory((prev) => [entry, ...prev]);
      setLatestResult(null);

      // Only send connection when the tool's schema accepts it — tools like
      // datahub_list_connections have an empty input schema and reject unexpected
      // properties.
      const sendConnection = "connection" in properties ? selectedConnection : "";

      callTool.mutate(
        {
          tool_name: selectedTool,
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
    },
    [selectedTool, selectedConnection, schema, callTool],
  );

  const handleReplay = useCallback(
    (entry: HistoryEntry) => {
      setSelectedTool(entry.tool_name);
      setSelectedConnection(entry.connection);
      setLatestResult(null);
      setShowRaw(false);
      setReplayParams(null);
      setReplaySource(null);
    },
    [],
  );

  return (
    <div className="space-y-4">
      <div className="flex gap-4" style={{ minHeight: 480 }}>
        {/* Tool Selector — left panel */}
        <div className="w-[280px] shrink-0 overflow-auto rounded-lg border bg-card">
          <div className="border-b p-3">
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search tools..."
              className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
            />
          </div>
          <div className="p-2">
            {filteredConnections.map((conn) => (
              <div key={conn.connection} className="mb-3">
                <div className="mb-1 flex items-center gap-2 px-2">
                  <StatusBadge variant="neutral">{conn.kind}</StatusBadge>
                  <span className="text-xs font-medium text-muted-foreground">
                    {conn.name}
                  </span>
                </div>
                {conn.tools.map((toolName) => {
                  const isHidden = hiddenToolSet.has(toolName);
                  return (
                    <button
                      key={`${conn.connection}-${toolName}`}
                      onClick={() => selectTool(toolName, conn)}
                      className={`flex w-full items-center gap-1.5 rounded-md px-3 py-1.5 text-left text-xs font-medium transition-colors ${
                        selectedTool === toolName &&
                        selectedConnection === conn.connection
                          ? "bg-primary/10 text-primary"
                          : "text-muted-foreground hover:bg-muted hover:text-foreground"
                      }`}
                    >
                      {isHidden ? (
                        <EyeOff className="h-3 w-3 shrink-0 opacity-40" />
                      ) : (
                        <Eye className="h-3 w-3 shrink-0 opacity-40" />
                      )}
                      <span className={isHidden ? "opacity-50" : ""}>{toolName}</span>
                    </button>
                  );
                })}
              </div>
            ))}
            {platformTools.length > 0 && (
              <div className="mb-3">
                <div className="mb-1 flex items-center gap-2 px-2">
                  <StatusBadge variant="neutral">platform</StatusBadge>
                  <span className="text-xs font-medium text-muted-foreground">
                    built-in
                  </span>
                </div>
                {platformTools.map((toolName) => (
                  <button
                    key={`platform-${toolName}`}
                    onClick={() => selectTool(toolName, null)}
                    className={`flex w-full items-center gap-1.5 rounded-md px-3 py-1.5 text-left text-xs font-medium transition-colors ${
                      selectedTool === toolName && selectedConnection === ""
                        ? "bg-primary/10 text-primary"
                        : "text-muted-foreground hover:bg-muted hover:text-foreground"
                    }`}
                  >
                    <Eye className="h-3 w-3 shrink-0 opacity-40" />
                    {toolName}
                  </button>
                ))}
              </div>
            )}
            {filteredConnections.length === 0 && platformTools.length === 0 && (
              <p className="px-2 py-4 text-center text-xs text-muted-foreground">
                No tools match
              </p>
            )}
          </div>
        </div>

        {/* Form & Result — right panel */}
        <div className="flex flex-1 flex-col gap-4 overflow-hidden">
          {schema ? (
            <>
              {/* Description */}
              <p className="text-sm text-muted-foreground">
                {schema.description}
              </p>

              {/* Dynamic form */}
              {/* Replay banner */}
              {replaySource && (
                <div className="flex items-center gap-3 rounded-lg border border-primary/20 bg-primary/5 px-4 py-2.5 text-sm">
                  <span className="flex-1 text-muted-foreground">
                    Replaying audit event{" "}
                    <code className="rounded bg-muted px-1 py-0.5 text-xs font-mono">
                      {replaySource.event_id.slice(0, 8)}
                    </code>{" "}
                    from {new Date(replaySource.event_timestamp).toLocaleString()}
                  </span>
                  <button
                    type="button"
                    onClick={() => { setReplaySource(null); setReplayParams(null); }}
                    className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                    title="Dismiss"
                  >
                    <X className="h-4 w-4" />
                  </button>
                </div>
              )}

              <form
                key={`${selectedTool}-${selectedConnection}-${formVersion}`}
                onSubmit={handleExecute}
                className="space-y-3 rounded-lg border bg-card p-4"
              >
                {Object.entries(schema.parameters.properties ?? {}).map(
                  ([key, prop]) => {
                    const isRequired =
                      (schema.parameters.required ?? []).includes(key);
                    if (key === "connection") {
                      return (
                        <div key={key}>
                          <label className="mb-1 block text-xs font-medium">
                            {key}
                          </label>
                          <p className="mb-1 text-[11px] text-muted-foreground">
                            {prop.description}
                          </p>
                          <select
                            disabled
                            value={selectedConnection}
                            className="rounded-md border bg-muted px-3 py-1.5 text-sm text-muted-foreground outline-none"
                          >
                            <option value={selectedConnection}>{selectedConnection}</option>
                          </select>
                        </div>
                      );
                    }
                    return (
                      <div key={key}>
                        <label className="mb-1 block text-xs font-medium">
                          {key}
                          {isRequired && (
                            <span className="ml-0.5 text-red-500">*</span>
                          )}
                        </label>
                        <p className="mb-1 text-[11px] text-muted-foreground">
                          {prop.description}
                        </p>
                        <FieldInput
                          name={key}
                          prop={prop}
                          required={isRequired}
                          initialValue={replayParams?.[key]}
                        />
                      </div>
                    );
                  },
                )}
                <button
                  type="submit"
                  disabled={callTool.isPending}
                  className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                >
                  {callTool.isPending ? "Executing..." : "Execute"}
                </button>
              </form>

              {/* Result panel */}
              {latestResult && (
                <ResultPanel
                  result={latestResult}
                  toolKind={schema.kind}
                  showRaw={showRaw}
                  onToggleRaw={() => setShowRaw((v) => !v)}
                />
              )}
            </>
          ) : (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">
              Select a tool from the left panel to begin testing
            </div>
          )}
        </div>
      </div>

      {/* History */}
      <div className="rounded-lg border bg-card">
        <button
          onClick={() => setHistoryOpen((v) => !v)}
          className="flex w-full items-center justify-between border-b p-3 text-sm font-medium"
        >
          <span>
            History{" "}
            <span className="text-muted-foreground">
              ({history.length})
            </span>
          </span>
          <span className="text-xs text-muted-foreground">
            {historyOpen ? "Collapse" : "Expand"}
          </span>
        </button>
        {historyOpen && (
          <div className="overflow-auto">
            {history.length === 0 ? (
              <p className="px-3 py-6 text-center text-sm text-muted-foreground">
                No test calls yet
              </p>
            ) : (
              <>
                <table className="w-full text-sm">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-3 py-2 text-left font-medium">
                        Timestamp
                      </th>
                      <th className="px-3 py-2 text-left font-medium">Tool</th>
                      <th className="px-3 py-2 text-left font-medium">
                        Connection
                      </th>
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
                        <td className="px-3 py-2 font-mono text-xs">
                          {entry.tool_name}
                        </td>
                        <td className="px-3 py-2 text-xs">
                          {entry.connection}
                        </td>
                        <td className="px-3 py-2 text-right text-xs">
                          {entry.is_loading
                            ? "..."
                            : entry.response
                              ? formatDuration(entry.response.duration_ms)
                              : "-"}
                        </td>
                        <td className="px-3 py-2 text-center">
                          {entry.is_loading ? (
                            <StatusBadge variant="warning">
                              Running
                            </StatusBadge>
                          ) : entry.response?.is_error ? (
                            <StatusBadge variant="error">Error</StatusBadge>
                          ) : (
                            <StatusBadge variant="success">
                              Success
                            </StatusBadge>
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

// ---------------------------------------------------------------------------
// FieldInput — renders the appropriate input for a tool parameter
// ---------------------------------------------------------------------------

function FieldInput({
  name,
  prop,
  required,
  initialValue,
}: {
  name: string;
  prop: { type: string; format?: string; enum?: string[]; default?: string | number | boolean; description: string };
  required: boolean;
  initialValue?: unknown;
}) {
  const resolvedDefault = initialValue !== undefined ? initialValue : prop.default;

  if (prop.type === "string" && prop.format === "sql") {
    return (
      <textarea
        name={name}
        required={required}
        rows={6}
        defaultValue={String(resolvedDefault ?? "")}
        className="w-full rounded-md border bg-background px-3 py-2 font-mono text-sm outline-none ring-ring focus:ring-2"
        placeholder="SELECT ..."
      />
    );
  }

  if (prop.type === "string" && prop.enum) {
    return (
      <select
        name={name}
        required={required}
        defaultValue={String(resolvedDefault ?? "")}
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">-- select --</option>
        {prop.enum.map((v) => (
          <option key={v} value={v}>
            {v}
          </option>
        ))}
      </select>
    );
  }

  if (prop.type === "string" && prop.format === "urn") {
    return (
      <input
        type="text"
        name={name}
        required={required}
        defaultValue={String(resolvedDefault ?? "")}
        className="w-full rounded-md border bg-background px-3 py-1.5 font-mono text-sm outline-none ring-ring focus:ring-2"
        placeholder="urn:li:dataset:..."
      />
    );
  }

  if (prop.type === "integer") {
    return (
      <input
        type="number"
        name={name}
        required={required}
        defaultValue={resolvedDefault !== undefined ? Number(resolvedDefault) : undefined}
        className="w-32 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      />
    );
  }

  if (prop.type === "boolean") {
    const checked = initialValue !== undefined ? Boolean(initialValue) : prop.default === true;
    return (
      <input
        type="checkbox"
        name={name}
        defaultChecked={checked}
        className="h-4 w-4 rounded border"
      />
    );
  }

  // Default: text input
  return (
    <input
      type="text"
      name={name}
      required={required}
      defaultValue={String(resolvedDefault ?? "")}
      className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
    />
  );
}

// ---------------------------------------------------------------------------
// ResultPanel — renders tool call results with formatted/raw toggle
// ---------------------------------------------------------------------------

/** Detect enrichment blocks by looking for known top-level keys. */
function detectEnrichmentLabel(text: string): string | null {
  try {
    const obj = JSON.parse(text);
    if (obj.semantic_context) return "Semantic Context";
    if (obj.query_context) return "Query Context";
    if (obj.column_context) return "Column Context";
    if (obj.storage_context) return "Storage Context";
    if (obj.metadata_reference) return "Metadata Reference";
  } catch {
    // not JSON — not enrichment
  }
  return null;
}

function ResultPanel({
  result,
  toolKind,
  showRaw,
  onToggleRaw,
}: {
  result: ToolCallResponse;
  toolKind: string;
  showRaw: boolean;
  onToggleRaw: () => void;
}) {
  // Separate primary result (first block) from enrichment blocks
  const hasContent = result.content.length > 0;
  const primary = result.content[0]?.text ?? "";
  const enrichmentBlocks = result.content.slice(1);
  const rawText = result.content.map((c) => c.text).join("\n\n");

  return (
    <div
      className={`rounded-lg border ${result.is_error ? "border-red-200 bg-red-50" : "bg-card"} p-4`}
    >
      {/* Header */}
      <div className="mb-3 flex items-center gap-3">
        <StatusBadge variant={result.is_error ? "error" : "success"}>
          {result.is_error ? "Error" : "Success"}
        </StatusBadge>
        <StatusBadge variant="neutral">{formatDuration(result.duration_ms)}</StatusBadge>
        {enrichmentBlocks.length > 0 && (
          <StatusBadge variant="success">Enriched</StatusBadge>
        )}
        <button
          onClick={onToggleRaw}
          className="ml-auto rounded px-2 py-0.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          {showRaw ? "Formatted" : "Raw"}
        </button>
      </div>

      {/* Content */}
      {!hasContent ? (
        <p className="text-sm text-muted-foreground">
          {result.is_error
            ? "Tool call failed. Check server logs for details."
            : "Tool returned no content."}
        </p>
      ) : showRaw ? (
        <pre className="max-h-[500px] overflow-auto rounded bg-muted p-3 font-mono text-xs">
          {rawText}
        </pre>
      ) : (
        <div className="space-y-4">
          {/* Primary tool result */}
          <div>
            <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Tool Result
            </p>
            <FormattedBlock text={primary} kind={toolKind} />
          </div>

          {/* Enrichment blocks */}
          {enrichmentBlocks.map((block, idx) => {
            const label = detectEnrichmentLabel(block.text) ?? `Enrichment ${idx + 1}`;
            return (
              <div key={idx} className="border-t pt-3">
                <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-primary">
                  {label}
                </p>
                <EnrichmentBlock text={block.text} />
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// FormattedBlock — render the primary tool result
// ---------------------------------------------------------------------------

function FormattedBlock({ text, kind }: { text: string; kind: string }) {
  if (!text) {
    return (
      <p className="text-sm italic text-muted-foreground">(empty response)</p>
    );
  }

  if (kind === "trino" && text.includes("|")) {
    return <MarkdownTableView text={text} />;
  }

  try {
    const parsed = JSON.parse(text);
    return (
      <pre className="max-h-[400px] overflow-auto rounded bg-muted p-3 font-mono text-xs">
        {JSON.stringify(parsed, null, 2)}
      </pre>
    );
  } catch {
    // plain text
  }

  return (
    <pre className="max-h-[400px] overflow-auto rounded bg-muted p-3 font-mono text-xs whitespace-pre-wrap">
      {text}
    </pre>
  );
}

// ---------------------------------------------------------------------------
// EnrichmentBlock — render enrichment JSON with structured sections
// ---------------------------------------------------------------------------

function EnrichmentBlock({ text }: { text: string }) {
  try {
    const obj = JSON.parse(text);
    return (
      <div className="space-y-2">
        {Object.entries(obj).map(([key, value]) => (
          <div key={key}>
            <p className="mb-1 text-xs font-medium text-muted-foreground">
              {key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())}
            </p>
            <pre className="max-h-[250px] overflow-auto rounded bg-primary/5 border border-primary/10 p-3 font-mono text-xs">
              {JSON.stringify(value, null, 2)}
            </pre>
          </div>
        ))}
      </div>
    );
  } catch {
    return (
      <pre className="max-h-[250px] overflow-auto rounded bg-muted p-3 font-mono text-xs whitespace-pre-wrap">
        {text}
      </pre>
    );
  }
}

// ---------------------------------------------------------------------------
// MarkdownTableView — renders markdown pipes as an HTML table
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Help Tab — Tools documentation
// ---------------------------------------------------------------------------

function ToolsHelpTab() {
  return (
    <div className="max-w-3xl space-y-8">
      <section>
        <h2 className="mb-2 text-lg font-semibold">What are MCP Tools?</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          MCP tools are the operations that AI assistants can invoke through the
          platform. Each tool performs a specific action &mdash; querying a
          database, searching a data catalog, reading from object storage, etc.
          Tools are organized into <strong>toolkits</strong> (Trino, DataHub, S3)
          and bound to <strong>connections</strong> (specific server instances).
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Connections & Toolkits</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          A <strong>connection</strong> represents a configured instance of an
          external service (e.g., a Trino cluster, a DataHub server, an S3
          endpoint). Each connection belongs to a <strong>toolkit</strong> type
          and exposes a set of tools. Multiple connections of the same type are
          supported &mdash; for example, you could have separate Trino
          connections for production and staging clusters.
        </p>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Toolkit</th>
                <th className="px-3 py-2 text-left font-medium">Tools Provided</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium">Trino</td>
                <td className="px-3 py-2 text-xs">
                  trino_query, trino_describe_table, trino_list_catalogs,
                  trino_list_schemas, trino_list_tables, trino_explain
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium">DataHub</td>
                <td className="px-3 py-2 text-xs">
                  datahub_search, datahub_get_entity, datahub_get_schema,
                  datahub_get_lineage, datahub_get_column_lineage,
                  datahub_get_glossary_term, datahub_get_queries,
                  datahub_get_data_product, datahub_list_data_products,
                  datahub_list_domains, datahub_list_tags
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-medium">S3</td>
                <td className="px-3 py-2 text-xs">
                  s3_list_buckets, s3_list_objects, s3_get_object,
                  s3_get_object_metadata, s3_put_object, s3_delete_object,
                  s3_copy_object, s3_presign_url
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Semantic Enrichment</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          A key differentiator of this platform is <strong>bidirectional
          cross-injection</strong>. Tool responses are automatically enriched
          with context from other services:
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Trino &rarr; DataHub</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              When you describe a table or run a query in Trino, the response
              includes DataHub metadata: table owners, tags, glossary terms,
              deprecation warnings, and data quality scores.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">DataHub &rarr; Trino</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              When searching DataHub, results include query availability: can
              this entity be queried? How many rows? What sample SQL to use?
              This helps the AI assistant know what data is actionable.
            </p>
          </div>
        </div>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Tool Access Control</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Tool access is controlled by <strong>personas</strong>. Each persona
          defines allow/deny patterns that filter which tools a user can invoke.
          When a tool call is attempted, the platform checks the user&apos;s
          persona and either permits or blocks the call. See the{" "}
          <strong>Personas</strong> section for details on how filtering works.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">The Explore Tab</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The Explore tab provides an interactive tool testing interface. Select
          a tool from the left panel, fill in parameters using the auto-generated
          form, and execute the call. Results are displayed with formatted output
          and enrichment blocks shown separately. A history panel tracks all
          calls made during the session with timing and status information.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Tool Schemas</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Each tool has a JSON Schema that defines its parameters (name, type,
          required, description, default values). The Explore tab uses these
          schemas to generate dynamic input forms. Parameter types include
          string, integer, boolean, and special formats like SQL, URN, and
          enum selections.
        </p>
      </section>
    </div>
  );
}

// ---------------------------------------------------------------------------
// MarkdownTableView — renders markdown pipes as an HTML table
// ---------------------------------------------------------------------------

function MarkdownTableView({ text }: { text: string }) {
  const lines = text.split("\n");
  const tableLines = lines.filter(
    (l) => l.trim().startsWith("|") && l.trim().endsWith("|"),
  );
  const otherLines = lines.filter(
    (l) => !(l.trim().startsWith("|") && l.trim().endsWith("|")),
  );

  if (tableLines.length < 2) {
    return (
      <pre className="max-h-[400px] overflow-auto rounded bg-muted p-3 font-mono text-xs whitespace-pre-wrap">
        {text}
      </pre>
    );
  }

  const parseRow = (line: string) =>
    line
      .split("|")
      .slice(1, -1)
      .map((c) => c.trim());

  const headers = parseRow(tableLines[0]!);
  // Skip separator row (index 1)
  const rows = tableLines.slice(2).map(parseRow);

  return (
    <div className="space-y-2">
      <div className="max-h-[400px] overflow-auto rounded border">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b bg-muted/50">
              {headers.map((h, i) => (
                <th key={i} className="px-3 py-2 text-left font-medium">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, ri) => (
              <tr key={ri} className="border-b">
                {row.map((cell, ci) => (
                  <td key={ci} className="px-3 py-1.5 font-mono">
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {otherLines.filter((l) => l.trim()).length > 0 && (
        <p className="text-xs text-muted-foreground">
          {otherLines
            .filter((l) => l.trim())
            .join(" ")}
        </p>
      )}
    </div>
  );
}

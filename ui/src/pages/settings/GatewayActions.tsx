import { useState, useCallback } from "react";
import {
  useTestGatewayConnection,
  useRefreshGatewayConnection,
  useEnrichmentRules,
} from "@/api/admin/hooks";
import type { EnrichmentRule, GatewayProbeTool } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { Plug, RefreshCw, Workflow, Plus, X } from "lucide-react";
import { RuleListItem } from "./gateway-actions/RuleListItem";
import { RuleEditor } from "./gateway-actions/RuleEditor";

// Re-export moved subcomponents so existing import paths keep working.
export { RuleListItem } from "./gateway-actions/RuleListItem";
export { RuleEditor } from "./gateway-actions/RuleEditor";
export { DryRunPanel } from "./gateway-actions/DryRunPanel";
export { Field, JSONField } from "./gateway-actions/Field";

// ---------------------------------------------------------------------------
// GatewayActionBar — buttons added to a gateway connection's viewer header
// ---------------------------------------------------------------------------

export function GatewayActionBar({
  connectionName,
  connectionConfig,
  onOpenRules,
}: {
  connectionName: string;
  connectionConfig: Record<string, unknown>;
  onOpenRules: () => void;
}) {
  const test = useTestGatewayConnection();
  const refresh = useRefreshGatewayConnection();

  const [testResult, setTestResult] = useState<{
    healthy: boolean;
    message: string;
    tools?: GatewayProbeTool[];
  } | null>(null);

  const handleTest = useCallback(async () => {
    setTestResult(null);
    try {
      const res = await test.mutateAsync({ name: connectionName, config: connectionConfig });
      setTestResult({
        healthy: res.healthy,
        message: res.healthy
          ? `Discovered ${res.tools?.length ?? 0} tools`
          : res.error ?? "Unknown error",
        tools: res.tools,
      });
    } catch (err) {
      setTestResult({ healthy: false, message: err instanceof Error ? err.message : "Test failed" });
    }
  }, [connectionName, connectionConfig, test]);

  const handleRefresh = useCallback(async () => {
    setTestResult(null);
    try {
      const res = await refresh.mutateAsync(connectionName);
      setTestResult({
        healthy: res.healthy,
        message: res.healthy
          ? `Refreshed; ${res.tools?.length ?? 0} tools registered`
          : res.error ?? "Refresh failed",
      });
    } catch (err) {
      setTestResult({ healthy: false, message: err instanceof Error ? err.message : "Refresh failed" });
    }
  }, [connectionName, refresh]);

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={handleTest}
          disabled={test.isPending}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
        >
          <Plug className="h-3 w-3" />
          {test.isPending ? "Testing..." : "Test connection"}
        </button>
        <button
          type="button"
          onClick={handleRefresh}
          disabled={refresh.isPending}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
        >
          <RefreshCw className={cn("h-3 w-3", refresh.isPending && "animate-spin")} />
          {refresh.isPending ? "Refreshing..." : "Refresh tools"}
        </button>
        <button
          type="button"
          onClick={onOpenRules}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <Workflow className="h-3 w-3" />
          Enrichment rules
        </button>
      </div>
      {testResult && (
        <div
          className={cn(
            "rounded-md border px-3 py-2 text-xs",
            testResult.healthy
              ? "border-emerald-500/30 bg-emerald-50 text-emerald-900 dark:bg-emerald-900/20 dark:text-emerald-200"
              : "border-destructive/30 bg-destructive/10 text-destructive",
          )}
        >
          <div className="font-medium">{testResult.healthy ? "Connected" : "Failed"}</div>
          <div className="mt-0.5">{testResult.message}</div>
          {testResult.tools && testResult.tools.length > 0 && (
            <details className="mt-1.5">
              <summary className="cursor-pointer text-xs uppercase tracking-wider opacity-70">
                Discovered tools
              </summary>
              <ul className="mt-1 space-y-0.5 font-mono">
                {testResult.tools.map((t) => (
                  <li key={t.local_name}>
                    {t.local_name}
                    {t.description && (
                      <span className="ml-2 text-xs opacity-70 font-sans">{t.description}</span>
                    )}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
      {/* OAuth status block is rendered by the parent
          ConnectionsPanel via ConnectionOAuthStatusCard so the SAME
          card appears for every connection kind, not just MCP. */}
    </div>
  );
}


// ---------------------------------------------------------------------------
// GatewayRulesDrawer — slide-out panel listing rules with edit / dry-run
// ---------------------------------------------------------------------------

export function GatewayRulesDrawer({
  connectionName,
  onClose,
}: {
  connectionName: string;
  onClose: () => void;
}) {
  const { data: rules, isLoading } = useEnrichmentRules(connectionName);
  const [editingRule, setEditingRule] = useState<EnrichmentRule | "new" | null>(null);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-2xl overflow-auto bg-card shadow-xl">
        <div className="sticky top-0 z-10 border-b bg-card px-6 py-4 flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold">Enrichment rules</h2>
            <p className="text-xs text-muted-foreground mt-0.5">
              for connection <span className="font-mono">{connectionName}</span>
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="p-6 space-y-4">
          {editingRule ? (
            <RuleEditor
              connectionName={connectionName}
              rule={editingRule === "new" ? null : editingRule}
              onClose={() => setEditingRule(null)}
            />
          ) : (
            <>
              <div className="flex justify-end">
                <button
                  type="button"
                  onClick={() => setEditingRule("new")}
                  className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90"
                >
                  <Plus className="h-3 w-3" />
                  New rule
                </button>
              </div>
              {isLoading ? (
                <div className="text-center text-sm text-muted-foreground py-8">Loading rules...</div>
              ) : !rules || rules.length === 0 ? (
                <div className="text-center text-sm text-muted-foreground py-8">
                  No enrichment rules. Click <strong>New rule</strong> to add one.
                </div>
              ) : (
                <ul className="space-y-2">
                  {rules.map((r) => (
                    <RuleListItem
                      key={r.id}
                      connectionName={connectionName}
                      rule={r}
                      onEdit={() => setEditingRule(r)}
                    />
                  ))}
                </ul>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

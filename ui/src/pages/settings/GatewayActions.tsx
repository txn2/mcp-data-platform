import { useState, useCallback } from "react";
import {
  useTestGatewayConnection,
  useRefreshGatewayConnection,
  useEnrichmentRules,
} from "@/api/admin/hooks";
import type { EnrichmentRule, GatewayProbeTool } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
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
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={handleTest}
          disabled={test.isPending}
        >
          <Plug />
          {test.isPending ? "Testing..." : "Test connection"}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={handleRefresh}
          disabled={refresh.isPending}
        >
          <RefreshCw className={cn(refresh.isPending && "animate-spin")} />
          {refresh.isPending ? "Refreshing..." : "Refresh tools"}
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={onOpenRules}>
          <Workflow />
          Enrichment rules
        </Button>
      </div>
      {testResult && (
        <Alert
          variant={testResult.healthy ? "success" : "destructive"}
          className="px-3 py-2"
        >
          <AlertTitle className="text-xs">
            {testResult.healthy ? "Connected" : "Failed"}
          </AlertTitle>
          <AlertDescription className="text-xs">
            <span>{testResult.message}</span>
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
                        <span className="ml-2 font-sans text-xs opacity-70">
                          {t.description}
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </AlertDescription>
        </Alert>
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
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={onClose}
            aria-label="Close"
          >
            <X />
          </Button>
        </div>

        <div className="space-y-4 p-6">
          {editingRule ? (
            <RuleEditor
              connectionName={connectionName}
              rule={editingRule === "new" ? null : editingRule}
              onClose={() => setEditingRule(null)}
            />
          ) : (
            <>
              <div className="flex justify-end">
                <Button type="button" size="sm" onClick={() => setEditingRule("new")}>
                  <Plus />
                  New rule
                </Button>
              </div>
              {isLoading ? (
                <p className="py-8 text-center text-sm text-muted-foreground">
                  Loading rules...
                </p>
              ) : !rules || rules.length === 0 ? (
                <EmptyState icon={Workflow}>
                  No enrichment rules. Click <strong>New rule</strong> to add one.
                </EmptyState>
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

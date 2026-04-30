import { useState, useCallback, useMemo } from "react";
import {
  useTestGatewayConnection,
  useRefreshGatewayConnection,
  useEnrichmentRules,
  useCreateEnrichmentRule,
  useUpdateEnrichmentRule,
  useDeleteEnrichmentRule,
  useDryRunEnrichmentRule,
  useGatewayConnectionStatus,
  useReacquireGatewayOAuth,
  useStartGatewayOAuth,
} from "@/api/admin/hooks";
import { ApiError } from "@/api/admin/client";
import type {
  EnrichmentRule,
  EnrichmentRuleBody,
  GatewayProbeTool,
  DryRunResponse,
  GatewayOAuthStatus,
} from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  Plug,
  RefreshCw,
  Workflow,
  Plus,
  Trash2,
  Save,
  X,
  Check,
  AlertCircle,
  Play,
  Key,
  Clock,
  KeyRound,
  ExternalLink,
} from "lucide-react";

// ---------------------------------------------------------------------------
// GatewayActionBar — buttons added to a gateway connection's viewer header
// ---------------------------------------------------------------------------

export function GatewayActionBar({
  connectionName,
  connectionConfig,
  onOpenRules,
}: {
  connectionName: string;
  connectionConfig: Record<string, any>;
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
      <OAuthStatusCard connectionName={connectionName} />
    </div>
  );
}

// ---------------------------------------------------------------------------
// OAuthStatusCard — token state + Reacquire button (rendered only when the
// connection's auth_mode is "oauth")
// ---------------------------------------------------------------------------

function OAuthStatusCard({ connectionName }: { connectionName: string }) {
  const { data: status, error } = useGatewayConnectionStatus(connectionName);
  const reacquire = useReacquireGatewayOAuth();
  const startOAuth = useStartGatewayOAuth();
  const [actionMsg, setActionMsg] = useState<{ ok: boolean; text: string } | null>(null);

  // Surface the "gateway toolkit is not registered" failure mode (HTTP 409
  // from /gateway/connections/{name}/status). Without this, the card
  // silently disappears and the operator has no signal that their saved
  // connection is inert because the gateway toolkit has been explicitly
  // disabled in platform.yaml. Auto-enable handles the no-config case;
  // this branch handles the explicit-disable case.
  if (error instanceof ApiError && error.status === 409) {
    return (
      <div className="rounded-md border border-amber-500/30 bg-amber-50 px-3 py-3 text-xs dark:bg-amber-900/20 dark:text-amber-200">
        <div className="flex items-center gap-2">
          <AlertCircle className="h-3.5 w-3.5" />
          <span className="font-semibold">Gateway toolkit disabled</span>
        </div>
        <p className="mt-1.5">
          This connection is saved in the database but not active: the gateway
          toolkit has been explicitly disabled in <code>platform.yaml</code>.
          Remove <code>toolkits.mcp.enabled: false</code> (or set it to{" "}
          <code>true</code>) and restart the platform to activate this
          connection. Tools from this upstream will not be available until
          then.
        </p>
      </div>
    );
  }

  if (!status || status.auth_mode !== "oauth" || !status.oauth) {
    return null;
  }
  const oauth = status.oauth;
  const isAuthCode = oauth.grant === "authorization_code";

  const handleReacquire = async () => {
    setActionMsg(null);
    try {
      await reacquire.mutateAsync(connectionName);
      setActionMsg({ ok: true, text: "Token refreshed" });
    } catch (err) {
      setActionMsg({ ok: false, text: err instanceof Error ? err.message : "Reacquire failed" });
    }
  };

  const handleConnect = async () => {
    setActionMsg(null);
    try {
      const res = await startOAuth.mutateAsync({
        name: connectionName,
        returnURL: window.location.pathname + window.location.search,
      });
      // Open the upstream's authorization URL in a new tab so the
      // operator can complete the browser dance without losing the
      // admin context. Status will auto-refetch on the existing 30s
      // poll once the callback runs.
      window.open(res.authorization_url, "_blank", "noopener,noreferrer");
      setActionMsg({
        ok: true,
        text: "Authorization page opened in a new tab. Sign in to complete the connection.",
      });
    } catch (err) {
      setActionMsg({ ok: false, text: err instanceof Error ? err.message : "Connect failed" });
    }
  };

  return (
    <div className="rounded-md border bg-muted/10 px-3 py-3 space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            OAuth status
          </span>
          <span className="rounded bg-muted text-muted-foreground px-1 py-0 text-xs font-medium font-mono">
            {oauth.grant}
          </span>
        </div>
        <div className="flex gap-1">
          {isAuthCode && (
            <button
              type="button"
              onClick={handleConnect}
              disabled={startOAuth.isPending}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium disabled:opacity-50",
                oauth.needs_reauth
                  ? "bg-primary text-primary-foreground border-primary hover:bg-primary/90"
                  : "text-muted-foreground hover:bg-muted hover:text-foreground",
              )}
            >
              <ExternalLink className="h-3 w-3" />
              {oauth.needs_reauth ? "Connect" : "Reconnect"}
            </button>
          )}
          {oauth.token_acquired && (
            <button
              type="button"
              onClick={handleReacquire}
              disabled={reacquire.isPending}
              className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
            >
              <RefreshCw className={cn("h-3 w-3", reacquire.isPending && "animate-spin")} />
              {reacquire.isPending ? "Refreshing..." : "Refresh now"}
            </button>
          )}
        </div>
      </div>

      {oauth.needs_reauth && (
        <div className="rounded border border-amber-500/30 bg-amber-50 px-2 py-1.5 text-xs text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
          <span className="font-medium">Not connected.</span> Click <strong>Connect</strong> to authorize this connection in your browser. The platform will then keep the access token refreshed automatically — including for cron jobs and scheduled prompts — until the upstream invalidates the refresh token.
        </div>
      )}

      <OAuthStatusGrid status={oauth} />

      {oauth.authenticated_by && (
        <div className="text-xs text-muted-foreground">
          Authorized by{" "}
          <span className="font-mono">{oauth.authenticated_by}</span>
          {oauth.authenticated_at && <> {formatRelative(oauth.authenticated_at)}</>}
        </div>
      )}

      {oauth.last_error && (
        <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive">
          <span className="font-medium">Last error:</span> {oauth.last_error}
        </div>
      )}

      {actionMsg && (
        <div
          className={cn(
            "rounded border px-2 py-1 text-xs",
            actionMsg.ok
              ? "border-emerald-500/30 bg-emerald-50 text-emerald-900 dark:bg-emerald-900/20 dark:text-emerald-200"
              : "border-destructive/30 bg-destructive/10 text-destructive",
          )}
        >
          {actionMsg.text}
        </div>
      )}
    </div>
  );
}

function OAuthStatusGrid({ status }: { status: GatewayOAuthStatus }) {
  const items: Array<{ label: string; value: string; icon: React.ReactNode; tone?: "ok" | "warn" }> = [
    {
      label: "Token",
      value: status.token_acquired ? "acquired" : "not yet acquired",
      icon: status.token_acquired ? (
        <Check className="h-3 w-3 text-emerald-500" />
      ) : (
        <AlertCircle className="h-3 w-3 text-amber-500" />
      ),
      tone: status.token_acquired ? "ok" : "warn",
    },
    {
      label: "Expires",
      value: status.expires_at ? formatRelative(status.expires_at) : "—",
      icon: <Clock className="h-3 w-3 text-muted-foreground" />,
    },
    {
      label: "Last refreshed",
      value: status.last_refreshed_at ? formatRelative(status.last_refreshed_at) : "—",
      icon: <RefreshCw className="h-3 w-3 text-muted-foreground" />,
    },
    {
      label: "Refresh token",
      value: status.has_refresh_token ? "present" : "none",
      icon: <Key className="h-3 w-3 text-muted-foreground" />,
    },
  ];
  return (
    <div className="grid grid-cols-2 gap-2 text-xs">
      {items.map((it) => (
        <div key={it.label} className="flex items-center gap-1.5">
          {it.icon}
          <span className="text-muted-foreground">{it.label}:</span>
          <span className="font-mono">{it.value}</span>
        </div>
      ))}
    </div>
  );
}

function formatRelative(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return iso;
  const diff = t - Date.now();
  const abs = Math.abs(diff);
  const sec = Math.round(abs / 1000);
  const min = Math.round(sec / 60);
  const hr = Math.round(min / 60);
  const day = Math.round(hr / 24);
  let rel: string;
  if (sec < 60) rel = `${sec}s`;
  else if (min < 60) rel = `${min}m`;
  else if (hr < 24) rel = `${hr}h`;
  else rel = `${day}d`;
  return diff >= 0 ? `in ${rel}` : `${rel} ago`;
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

// ---------------------------------------------------------------------------
// RuleListItem — one rule with summary + edit/delete buttons
// ---------------------------------------------------------------------------

function RuleListItem({
  connectionName,
  rule,
  onEdit,
}: {
  connectionName: string;
  rule: EnrichmentRule;
  onEdit: () => void;
}) {
  const del = useDeleteEnrichmentRule(connectionName);
  const [confirmDelete, setConfirmDelete] = useState(false);

  return (
    <li className="rounded-md border p-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="font-mono text-xs">{rule.tool_name}</span>
            <span
              className={cn(
                "rounded px-1.5 py-0 text-xs font-medium",
                rule.enabled
                  ? "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400"
                  : "bg-muted text-muted-foreground",
              )}
            >
              {rule.enabled ? "enabled" : "disabled"}
            </span>
          </div>
          {rule.description && (
            <p className="mt-1 text-xs text-muted-foreground">{rule.description}</p>
          )}
          <p className="mt-1 text-xs text-muted-foreground font-mono">
            {rule.enrich_action.source}.{rule.enrich_action.operation} →{" "}
            {rule.merge_strategy.path || "enrichment"}
          </p>
        </div>
        <div className="flex gap-1 shrink-0">
          <button
            type="button"
            onClick={onEdit}
            className="rounded-md border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            Edit
          </button>
          {confirmDelete ? (
            <>
              <button
                type="button"
                onClick={async () => {
                  await del.mutateAsync(rule.id);
                  setConfirmDelete(false);
                }}
                className="rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground hover:bg-destructive/90"
              >
                Confirm
              </button>
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                className="rounded-md border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-muted"
              >
                Cancel
              </button>
            </>
          ) : (
            <button
              type="button"
              onClick={() => setConfirmDelete(true)}
              className="rounded-md border px-2 py-1 text-xs font-medium text-muted-foreground hover:bg-destructive/10 hover:text-destructive hover:border-destructive/30"
            >
              <Trash2 className="h-3 w-3" />
            </button>
          )}
        </div>
      </div>
    </li>
  );
}

// ---------------------------------------------------------------------------
// RuleEditor — create/edit a single rule with JSON editors + dry-run panel
// ---------------------------------------------------------------------------

function emptyRuleBody(): EnrichmentRuleBody {
  return {
    tool_name: "",
    when_predicate: { kind: "always" },
    enrich_action: { source: "trino", operation: "query", parameters: {} },
    merge_strategy: { kind: "path", path: "enrichment" },
    description: "",
    enabled: true,
  };
}

function RuleEditor({
  connectionName,
  rule,
  onClose,
}: {
  connectionName: string;
  rule: EnrichmentRule | null;
  onClose: () => void;
}) {
  const create = useCreateEnrichmentRule(connectionName);
  const update = useUpdateEnrichmentRule(connectionName);

  const initialBody = useMemo<EnrichmentRuleBody>(() => {
    if (!rule) return emptyRuleBody();
    return {
      tool_name: rule.tool_name,
      when_predicate: rule.when_predicate,
      enrich_action: rule.enrich_action,
      merge_strategy: rule.merge_strategy,
      description: rule.description ?? "",
      enabled: rule.enabled,
    };
  }, [rule]);

  const [body, setBody] = useState<EnrichmentRuleBody>(initialBody);
  const [error, setError] = useState<string | null>(null);

  const handleSave = useCallback(async () => {
    setError(null);
    try {
      if (rule) {
        await update.mutateAsync({ id: rule.id, ...body });
      } else {
        await create.mutateAsync(body);
      }
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Save failed");
    }
  }, [rule, body, create, update, onClose]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">{rule ? "Edit rule" : "New rule"}</h3>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSave}
            disabled={create.isPending || update.isPending}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            <Save className="h-3 w-3" />
            {rule ? "Update" : "Create"}
          </button>
        </div>
      </div>

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="h-3.5 w-3.5 mt-0.5 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <Field label="Tool name" hint="The proxied tool this rule applies to (e.g. crm__get_contact).">
        <input
          type="text"
          className="w-full rounded-md border bg-background px-2 py-1 text-xs font-mono"
          value={body.tool_name}
          onChange={(e) => setBody({ ...body, tool_name: e.target.value })}
          placeholder={`${connectionName}__some_tool`}
        />
      </Field>

      <Field label="Description">
        <input
          type="text"
          className="w-full rounded-md border bg-background px-2 py-1 text-xs"
          value={body.description ?? ""}
          onChange={(e) => setBody({ ...body, description: e.target.value })}
          placeholder="What this rule does"
        />
      </Field>

      <Field label="Enabled">
        <label className="inline-flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            checked={body.enabled}
            onChange={(e) => setBody({ ...body, enabled: e.target.checked })}
          />
          Rule fires on matching tool calls
        </label>
      </Field>

      <JSONField
        label="When predicate"
        hint='Examples: {"kind":"always"} or {"kind":"response_contains","paths":["$.email"]}'
        value={body.when_predicate}
        onChange={(v) => setBody({ ...body, when_predicate: v })}
      />

      <JSONField
        label="Enrich action"
        hint='source must be "trino" or "datahub". String parameters starting with $. are JSONPath bindings against {args, response, user}.'
        value={body.enrich_action}
        onChange={(v) => setBody({ ...body, enrich_action: v })}
      />

      <JSONField
        label="Merge strategy"
        hint='{"kind":"path","path":"warehouse_signals"} attaches the source result under response.warehouse_signals.'
        value={body.merge_strategy}
        onChange={(v) => setBody({ ...body, merge_strategy: v })}
      />

      {rule && (
        <div className="border-t pt-4">
          <DryRunPanel connectionName={connectionName} ruleId={rule.id} />
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// DryRunPanel — sample input + result preview
// ---------------------------------------------------------------------------

function DryRunPanel({ connectionName, ruleId }: { connectionName: string; ruleId: string }) {
  const dryRun = useDryRunEnrichmentRule(connectionName);
  const [argsJSON, setArgsJSON] = useState('{"id": 7}');
  const [respJSON, setRespJSON] = useState('{"email": "x@x.com"}');
  const [userEmail, setUserEmail] = useState("admin@example.com");
  const [result, setResult] = useState<DryRunResponse | null>(null);
  const [parseError, setParseError] = useState<string | null>(null);

  const handleRun = useCallback(async () => {
    setParseError(null);
    setResult(null);
    let args: Record<string, any>;
    let resp: any;
    try {
      args = JSON.parse(argsJSON);
      resp = JSON.parse(respJSON);
    } catch (err) {
      setParseError(err instanceof Error ? err.message : "Invalid JSON");
      return;
    }
    try {
      const r = await dryRun.mutateAsync({
        id: ruleId,
        body: { args, response: resp, user: { email: userEmail } },
      });
      setResult(r);
    } catch (err) {
      setParseError(err instanceof Error ? err.message : "Dry-run failed");
    }
  }, [argsJSON, respJSON, userEmail, ruleId, dryRun]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Dry run
        </h4>
        <button
          type="button"
          onClick={handleRun}
          disabled={dryRun.isPending}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          <Play className="h-3 w-3" />
          {dryRun.isPending ? "Running..." : "Run"}
        </button>
      </div>

      <Field label="Sample args (JSON)">
        <textarea
          className="w-full rounded-md border bg-background px-2 py-1 text-xs font-mono"
          rows={3}
          value={argsJSON}
          onChange={(e) => setArgsJSON(e.target.value)}
        />
      </Field>

      <Field label="Sample response (JSON)">
        <textarea
          className="w-full rounded-md border bg-background px-2 py-1 text-xs font-mono"
          rows={3}
          value={respJSON}
          onChange={(e) => setRespJSON(e.target.value)}
        />
      </Field>

      <Field label="User email">
        <input
          type="text"
          className="w-full rounded-md border bg-background px-2 py-1 text-xs"
          value={userEmail}
          onChange={(e) => setUserEmail(e.target.value)}
        />
      </Field>

      {parseError && (
        <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {parseError}
        </div>
      )}

      {result && (
        <div className="space-y-2">
          {result.fired && result.fired.length > 0 && (
            <div className="rounded-md border px-3 py-2 text-xs">
              <div className="font-semibold mb-1">Trace</div>
              <ul className="space-y-1 font-mono text-xs">
                {result.fired.map((f) => (
                  <li key={f.rule_id} className="flex items-center gap-2">
                    {f.skipped ? (
                      <span className="text-muted-foreground">⊘</span>
                    ) : f.error ? (
                      <AlertCircle className="h-3 w-3 text-destructive" />
                    ) : (
                      <Check className="h-3 w-3 text-emerald-500" />
                    )}
                    <span>{f.source}.{f.op}</span>
                    <span className="text-muted-foreground">{f.duration_ms}ms</span>
                    {f.error && <span className="text-destructive">{f.error}</span>}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {result.warnings && result.warnings.length > 0 && (
            <div className="rounded-md border border-amber-500/30 bg-amber-50 px-3 py-2 text-xs dark:bg-amber-900/20">
              <div className="font-semibold mb-1">Warnings</div>
              <ul className="list-disc list-inside space-y-0.5">
                {result.warnings.map((w, i) => (
                  <li key={i}>{w}</li>
                ))}
              </ul>
            </div>
          )}
          <div>
            <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-1">
              Merged response
            </div>
            <pre className="rounded-md border bg-muted/30 p-2 text-xs font-mono overflow-x-auto">
              {JSON.stringify(result.response, null, 2)}
            </pre>
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Field + JSONField primitives
// ---------------------------------------------------------------------------

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </label>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

function JSONField<T>({
  label,
  hint,
  value,
  onChange,
}: {
  label: string;
  hint?: string;
  value: T;
  onChange: (v: T) => void;
}) {
  const [text, setText] = useState(() => JSON.stringify(value, null, 2));
  const [error, setError] = useState<string | null>(null);

  const handleChange = useCallback(
    (next: string) => {
      setText(next);
      try {
        onChange(JSON.parse(next) as T);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Invalid JSON");
      }
    },
    [onChange],
  );

  return (
    <Field label={label} hint={hint}>
      <textarea
        className={cn(
          "w-full rounded-md border bg-background px-2 py-1 text-xs font-mono",
          error && "border-destructive",
        )}
        rows={5}
        value={text}
        onChange={(e) => handleChange(e.target.value)}
      />
      {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
    </Field>
  );
}

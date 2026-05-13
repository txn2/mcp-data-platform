// ConnectionOAuthStatusCard renders the OAuth 2.1 authorization_code
// state for ANY connection kind. Driven by the unified
// /api/v1/admin/connections/{kind}/{name}/oauth-status endpoint, so the
// same visual surface appears for MCP-kind and API-kind connections —
// the consistency that was missing when this lived only in the MCP
// gateway view.
import { useState } from "react";
import {
  useConnectionOAuthStatus,
  useReacquireConnectionOAuth,
  useStartConnectionOAuth,
} from "@/api/admin/hooks";
import type { ConnectionOAuthStatus } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  AlertCircle,
  Check,
  Clock,
  ExternalLink,
  Key,
  KeyRound,
  RefreshCw,
} from "lucide-react";

interface Props {
  kind: string;
  name: string;
  // authMode is the connection's auth_mode value (from the connection
  // record itself). The parent decides up-front whether this card is
  // relevant — the card no longer "self-hides" based on an async
  // status fetch, which previously caused the entire OAuth section to
  // silently disappear on a slow / failed / loading status response.
  authMode: string;
}

const OAUTH_AUTH_MODES = new Set(["oauth", "oauth2_authorization_code"]);

export function ConnectionOAuthStatusCard({ kind, name, authMode }: Props) {
  // Render NOTHING (intentionally) only when the connection is not an
  // OAuth-mode at all. Past this gate, the card always renders — even
  // while the status fetch is loading or errored — so the operator is
  // never left wondering "where did the OAuth section go?".
  if (!OAUTH_AUTH_MODES.has(authMode)) {
    return null;
  }
  // Key on (kind, name) so React unmounts/remounts the inner card
  // when the operator switches connections in the sidebar. Without
  // the key, React reuses the same instance and the inner useState
  // (actionMsg) plus the mutation hooks' state (isSuccess flags, last
  // error) bleed across connections — e.g., refreshing the API
  // token's "Token refreshed" success banner would still show after
  // clicking the MCP connection, even though no MCP refresh happened.
  return <Inner key={`${kind}/${name}`} kind={kind} name={name} />;
}

function Inner({ kind, name }: { kind: string; name: string }) {
  const { data: status, isLoading, error } = useConnectionOAuthStatus(kind, name);
  const reacquire = useReacquireConnectionOAuth();
  const startOAuth = useStartConnectionOAuth(kind);
  const [actionMsg, setActionMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const handleConnect = async () => {
    setActionMsg(null);
    try {
      const res = await startOAuth.mutateAsync({
        name,
        returnURL: window.location.pathname + window.location.search,
      });
      if (!/^https?:\/\//i.test(res.authorization_url)) {
        setActionMsg({
          ok: false,
          text: "Server returned an invalid authorization URL. Check the connection's authorization_url field.",
        });
        return;
      }
      window.location.href = res.authorization_url;
    } catch (err) {
      setActionMsg({
        ok: false,
        text: err instanceof Error ? err.message : "Connect failed",
      });
    }
  };

  const handleReacquire = async () => {
    setActionMsg(null);
    try {
      await reacquire.mutateAsync({ kind, name });
      setActionMsg({ ok: true, text: "Token refreshed" });
    } catch (err) {
      setActionMsg({
        ok: false,
        text: err instanceof Error ? err.message : "Reacquire failed",
      });
    }
  };

  const tokenAcquired = status?.token_acquired ?? false;
  const needsReauth = status?.needs_reauth ?? !status; // assume reauth needed until we know otherwise

  return (
    <div className="rounded-md border bg-muted/10 px-3 py-3 space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            OAuth status
          </span>
          <span className="rounded bg-muted text-muted-foreground px-1 py-0 text-[11px] font-medium font-mono">
            authorization_code
          </span>
        </div>
        <div className="flex gap-1">
          <button
            type="button"
            onClick={handleConnect}
            disabled={startOAuth.isPending}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium disabled:opacity-50",
              needsReauth
                ? "bg-primary text-primary-foreground border-primary hover:bg-primary/90"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
            )}
          >
            <ExternalLink className="h-3 w-3" />
            {needsReauth ? "Connect" : "Reconnect"}
          </button>
          {tokenAcquired && (
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

      {isLoading && (
        <div className="text-xs text-muted-foreground">Loading OAuth status…</div>
      )}

      {error && (
        <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive">
          <span className="font-medium">Status unavailable:</span>{" "}
          {error instanceof Error ? error.message : "fetch failed"}
        </div>
      )}

      {status?.needs_reauth && (
        <div className="rounded border border-amber-500/30 bg-amber-50 px-2 py-1.5 text-xs text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
          {status.token_acquired ? (
            <>
              <span className="font-medium">Reauth needed soon.</span> The current
              access token still works, but the refresh-token deadline has passed.
              Click <strong>Connect</strong> to issue a fresh credential.
            </>
          ) : (
            <>
              <span className="font-medium">Not connected.</span> Click{" "}
              <strong>Connect</strong> to authorize this connection in your
              browser. The platform will then keep the access token refreshed
              automatically — including for cron jobs and scheduled prompts —
              until the upstream invalidates the refresh token.
            </>
          )}
        </div>
      )}

      {status && <StatusGrid status={status} />}

      {status?.authenticated_by && (
        <div className="text-xs text-muted-foreground">
          Authorized by <span className="font-mono">{status.authenticated_by}</span>
          {status.authenticated_at && <> {formatRelative(status.authenticated_at)}</>}
        </div>
      )}

      {status?.last_error && (
        <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive">
          <span className="font-medium">Last error:</span> {status.last_error}
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

function StatusGrid({ status }: { status: ConnectionOAuthStatus }) {
  const items: Array<{ label: string; value: string; icon: React.ReactNode }> = [
    {
      label: "Token",
      value: status.token_acquired ? "acquired" : "not yet acquired",
      icon: status.token_acquired ? (
        <Check className="h-3 w-3 text-emerald-500" />
      ) : (
        <AlertCircle className="h-3 w-3 text-amber-500" />
      ),
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
  if (status.has_refresh_token && status.refresh_expires_at) {
    items.push({
      label: "Refresh expires",
      value: formatRelative(status.refresh_expires_at),
      icon: <Clock className="h-3 w-3 text-muted-foreground" />,
    });
  }
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

// formatRelative renders an ISO-8601 timestamp as a coarse "in N
// minutes" or "N minutes ago" string. Same logic as the prior
// per-kind GatewayActions component.
function formatRelative(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const diff = date.getTime() - Date.now();
  const abs = Math.abs(diff);
  const suffix = diff < 0 ? "ago" : "from now";
  const minutes = Math.round(abs / 60_000);
  if (minutes < 1) return diff < 0 ? "just now" : "moments";
  if (minutes < 60) return `${minutes}m ${suffix}`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ${suffix}`;
  const days = Math.round(hours / 24);
  return `${days}d ${suffix}`;
}

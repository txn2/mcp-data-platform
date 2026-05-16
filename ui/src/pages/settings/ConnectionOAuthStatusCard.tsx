// ConnectionOAuthStatusCard renders the OAuth 2.1 authorization_code
// state for ANY connection kind. Driven by the unified
// /api/v1/admin/connections/{kind}/{name}/oauth-status endpoint, so the
// same visual surface appears for MCP-kind and API-kind connections —
// the consistency that was missing when this lived only in the MCP
// gateway view.
import { useState } from "react";
import {
  useConnectionAuthEvents,
  useConnectionOAuthStatus,
  useReacquireConnectionOAuth,
  useStartConnectionOAuth,
} from "@/api/admin/hooks";
import { ApiError } from "@/api/admin/client";
import type { ConnectionAuthEvent, ConnectionOAuthStatus } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  AlertCircle,
  AlertTriangle,
  Check,
  ChevronDown,
  ChevronRight,
  Clock,
  ExternalLink,
  History,
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
      setActionMsg({ ok: false, text: formatActionError(err, "Connect failed") });
    }
  };

  const handleReacquire = async () => {
    setActionMsg(null);
    try {
      await reacquire.mutateAsync({ kind, name });
      setActionMsg({ ok: true, text: "Token refreshed" });
    } catch (err) {
      setActionMsg({ ok: false, text: formatActionError(err, "Refresh failed") });
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
          {formatActionError(error, "fetch failed")}
        </div>
      )}

      {status?.needs_reauth && (
        <ConnectionStatePrompt status={status} />
      )}

      {status && <StatusGrid status={status} />}

      {status?.authenticated_by && (
        <div className="text-xs text-muted-foreground">
          Authorized by <span className="font-mono">{status.authenticated_by}</span>
          {status.authenticated_at && <> {formatRelative(status.authenticated_at)}</>}
        </div>
      )}

      {status?.last_error && status.last_error.trim() !== "" && (
        <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1 text-xs text-destructive">
          <span className="font-medium">Last error:</span> {status.last_error}
        </div>
      )}

      {actionMsg && actionMsg.text.trim() !== "" && (
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

      <AuthEventHistory kind={kind} name={name} />
    </div>
  );
}

// ConnectionStatePrompt renders the appropriate explainer band for the
// three distinct needs-reauth states. Distinguishing them is the
// difference between an operator panicking ("the token vanished!")
// and an operator nodding ("ah, the IdP rejected the refresh, I need
// to reconnect"). See issue #395 Part 3.
function ConnectionStatePrompt({ status }: { status: ConnectionOAuthStatus }) {
  if (status.last_revocation) {
    return <RevocationPrompt revocation={status.last_revocation} />;
  }
  if (status.token_acquired) {
    return (
      <div className="rounded border border-amber-500/30 bg-amber-50 px-2 py-1.5 text-xs text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
        <span className="font-medium">Reauth needed soon.</span> The current
        access token still works, but the refresh-token deadline has passed.
        Click <strong>Connect</strong> to issue a fresh credential.
      </div>
    );
  }
  return (
    <div className="rounded border border-amber-500/30 bg-amber-50 px-2 py-1.5 text-xs text-amber-900 dark:bg-amber-900/20 dark:text-amber-200">
      <span className="font-medium">Not connected.</span> Click{" "}
      <strong>Connect</strong> to authorize this connection in your
      browser. The platform will then keep the access token refreshed
      automatically — including for cron jobs and scheduled prompts —
      until the upstream invalidates the refresh token.
    </div>
  );
}

// RevocationPrompt renders the explainer for a connection whose last
// known state is "token deleted by the platform." The reason field
// tells us how the verdict was reached, and the wording differs
// substantially:
//
//   - refresh_expired: NO IdP call happened. The previous successful
//     refresh response disclosed a hard deadline via refresh_expires_in
//     (e.g., Keycloak SsoSessionMaxLifespan), the deadline arrived,
//     and the platform stopped before contacting the IdP. Saying the
//     IdP "returned refresh_expired" is wrong — the IdP wasn't asked.
//
//   - invalid_grant: the IdP was called and returned RFC 6749 §5.2
//     invalid_grant. The session is genuinely terminated upstream
//     (operator revoked consent, replay protection fired, etc.).
//
//   - no_refresh_token: there was no refresh token to exchange. Always
//     a local determination — never reached the IdP.
//
// Each case gets its own headline and explanation so operators can
// tell whether the IdP rejected something or the platform respected a
// deadline.
function RevocationPrompt({
  revocation,
}: {
  revocation: NonNullable<ConnectionOAuthStatus["last_revocation"]>;
}) {
  const reason = revocation.reason;
  const host = revocation.idp_host;
  const occurred = formatRelative(revocation.occurred_at);
  return (
    <div className="rounded border border-destructive/30 bg-destructive/10 px-2 py-1.5 text-xs text-destructive">
      <div className="flex items-start gap-1.5">
        <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0 mt-0.5" />
        <div>
          <span className="font-medium">{revocationHeadline(reason)}</span>{" "}
          <RevocationBody reason={reason} host={host} />
          {" "}
          <span className="text-muted-foreground">({occurred})</span>
          {" "}Click <strong>Connect</strong> to re-authorize.
        </div>
      </div>
    </div>
  );
}

// revocationHeadline returns the short, bold lead text for each
// revocation reason. Exported via module scope so the test file can
// assert exact wording without rendering.
export function revocationHeadline(reason: string | undefined): string {
  switch (reason) {
    case "refresh_expired":
      return "Session reached the IdP-disclosed maximum lifetime.";
    case "invalid_grant":
      return "Upstream IdP rejected the refresh token.";
    case "no_refresh_token":
      return "No refresh token is stored for this connection.";
    default:
      return "Previous session ended.";
  }
}

function RevocationBody({
  reason,
  host,
}: {
  reason: string | undefined;
  host: string | undefined;
}) {
  const idp = host ? (
    <span className="font-mono">{host}</span>
  ) : (
    <>the upstream IdP</>
  );
  switch (reason) {
    case "refresh_expired":
      return (
        <>
          The previous successful refresh from {idp} disclosed this
          deadline (typically the IdP's session-lifetime ceiling), so
          the platform did not attempt another refresh.
        </>
      );
    case "invalid_grant":
      return (
        <>
          {idp} returned <span className="font-mono">invalid_grant</span>.
          Common causes: the operator revoked consent, the IdP detected
          replay of a rotated single-use refresh token, or the session
          was administratively terminated.
        </>
      );
    case "no_refresh_token":
      return (
        <>
          The platform had no refresh token to exchange with {idp}.
        </>
      );
    default:
      return (
        <>
          {idp} could not extend the session.
        </>
      );
  }
}

// AuthEventHistory is the collapsible History section under the OAuth
// status card. Renders the most recent 30 lifecycle events so operators
// can answer "when did this connection's token last refresh, and what
// triggered the previous deletion?" without opening pod logs. Hidden
// by default — most operators don't need the detail except when
// debugging.
function AuthEventHistory({ kind, name }: { kind: string; name: string }) {
  const [open, setOpen] = useState(false);
  const { data: events, isLoading } = useConnectionAuthEvents(kind, name, open);
  const list = events ?? [];
  return (
    <div className="rounded-md border bg-muted/10 px-2 py-1.5">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
      >
        {open ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
        <History className="h-3 w-3" />
        <span>History</span>
        {open && !isLoading && (
          <span className="ml-1 text-muted-foreground/70">({list.length})</span>
        )}
      </button>
      {open && (
        <div className="mt-2 space-y-1">
          {isLoading && (
            <div className="text-xs text-muted-foreground">Loading…</div>
          )}
          {!isLoading && list.length === 0 && (
            <div className="text-xs text-muted-foreground">
              No events recorded yet for this connection.
            </div>
          )}
          {!isLoading && list.map((ev) => (
            <AuthEventRow key={ev.id} event={ev} />
          ))}
        </div>
      )}
    </div>
  );
}

// EVENT_LABELS is the operator-facing label for each event type. The
// distinction between "failed (revoked)" and the two "skipped" types
// matters: failed_revoked means the IdP was called and rejected the
// refresh; the skipped types mean the platform reached the verdict
// without contacting the IdP (deadline disclosed by a previous
// successful refresh, or no refresh token was stored).
const EVENT_LABELS: Record<ConnectionAuthEvent["event_type"], string> = {
  connect_started: "Connect started",
  connect_completed: "Connect completed",
  refresh_succeeded: "Refresh succeeded",
  refresh_failed_transient: "Refresh failed (transient)",
  refresh_failed_revoked: "Refresh rejected by IdP",
  refresh_skipped_no_token: "Refresh skipped — no refresh token stored",
  refresh_skipped_expired: "Refresh skipped — IdP-disclosed deadline reached",
  refresh_rotation_persistence_failed: "Rotated token persistence failed",
  token_deleted_revoked: "Token row deleted",
  token_deleted_admin: "Token row deleted — admin",
};

const EVENT_TONE: Record<ConnectionAuthEvent["event_type"], string> = {
  connect_started: "text-muted-foreground",
  connect_completed: "text-emerald-600 dark:text-emerald-400",
  refresh_succeeded: "text-emerald-600 dark:text-emerald-400",
  refresh_failed_transient: "text-amber-600 dark:text-amber-400",
  refresh_failed_revoked: "text-destructive",
  refresh_skipped_no_token: "text-amber-600 dark:text-amber-400",
  refresh_skipped_expired: "text-amber-600 dark:text-amber-400",
  refresh_rotation_persistence_failed: "text-destructive font-medium",
  token_deleted_revoked: "text-destructive",
  token_deleted_admin: "text-muted-foreground",
};

function AuthEventRow({ event }: { event: ConnectionAuthEvent }) {
  const label = EVENT_LABELS[event.event_type] || event.event_type;
  const tone = EVENT_TONE[event.event_type] || "text-muted-foreground";
  const detail = renderDetailHint(event);
  return (
    <div className="flex items-baseline gap-2 text-xs">
      <span className="text-muted-foreground font-mono w-20 flex-shrink-0">
        {formatRelative(event.occurred_at)}
      </span>
      <span className={cn("font-medium", tone)}>{label}</span>
      <span className="text-muted-foreground/70 font-mono text-[11px] truncate">
        {event.actor}
      </span>
      {detail && (
        <span className="text-muted-foreground/70 truncate">{detail}</span>
      )}
    </div>
  );
}

// renderDetailHint produces a one-line detail string for an event,
// pulling the most relevant detail fields per Type. Returns empty
// string when the row has nothing extra worth showing (a clean
// connect_started, for example).
export function renderDetailHint(ev: ConnectionAuthEvent): string {
  if (!ev.detail) return "";
  const d = ev.detail as Record<string, unknown>;
  if (typeof d.idp_error_code === "string" && d.idp_error_code) {
    return `(${describeVerdictCode(d.idp_error_code)})`;
  }
  if (typeof d.reason === "string" && d.reason) {
    return `(${describeVerdictCode(d.reason)})`;
  }
  if (d.rotated_refresh === true) {
    const ms = typeof d.duration_ms === "number" ? `, ${d.duration_ms}ms` : "";
    return `(rotated refresh${ms})`;
  }
  if (typeof d.duration_ms === "number" && d.duration_ms > 0) {
    return `(${d.duration_ms}ms)`;
  }
  return "";
}

// describeVerdictCode translates the short reason codes stored in
// event detail into honest one-line labels. The backend currently
// stores `refresh_expired` and `no_refresh_token` for verdicts the
// platform reached without contacting the IdP — surfacing the raw
// code reads as "the IdP returned this," which is incorrect. The
// IdP-returned code `invalid_grant` passes through verbatim because
// it IS what the IdP returned.
export function describeVerdictCode(code: string): string {
  switch (code) {
    case "refresh_expired":
      return "IdP-disclosed deadline reached";
    case "no_refresh_token":
      return "no refresh token stored";
    default:
      return code;
  }
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

// formatActionError turns any thrown value into a non-empty,
// operator-meaningful string. The previous render used
// `err.message` directly, which produced an empty red box when an
// ApiError carried an empty detail (some upstream paths return 502
// with no body, and HTTP/2 fetches return empty statusText). An
// empty error box is the worst of both worlds: the operator sees
// something went wrong but learns nothing about what. Always fall
// back to the caller's label, and append the HTTP status when
// available so "Refresh failed (HTTP 502)" beats a blank box.
export function formatActionError(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    const detail = err.detail?.trim();
    if (detail) return detail;
    return err.status > 0 ? `${fallback} (HTTP ${err.status})` : fallback;
  }
  if (err instanceof Error) {
    const msg = err.message?.trim();
    if (msg) return msg;
  }
  return fallback;
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

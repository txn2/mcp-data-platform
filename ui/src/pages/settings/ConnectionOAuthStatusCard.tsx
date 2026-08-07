// ConnectionOAuthStatusCard renders the OAuth 2.1 authorization_code
// state for ANY connection kind. Driven by the unified
// /api/v1/admin/connections/{kind}/{name}/oauth-status endpoint, so the
// same visual surface appears for MCP-kind and API-kind connections —
// the consistency that was missing when this lived only in the MCP
// gateway view. The event history, the needs-reauth explainers, the
// status grid, and the text helpers live under ./connoauth/ (#1206).
import { useState } from "react";
import {
  useConnectionOAuthStatus,
  useReacquireConnectionOAuth,
  useStartConnectionOAuth,
} from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ExternalLink, KeyRound, RefreshCw } from "lucide-react";
import { AuthEventHistory } from "./connoauth/AuthEventHistory";
import { ConnectionStatePrompt } from "./connoauth/ConnectionStatePrompt";
import { StatusGrid } from "./connoauth/StatusGrid";
import { formatActionError, formatRelative } from "./connoauth/format";

// The card's text helpers and the revocation headline keep their historical
// import path: the test suite asserts their exact wording from this module.
export {
  describeVerdictCode,
  formatActionError,
  renderDetailHint,
} from "./connoauth/format";
export { revocationHeadline } from "./connoauth/ConnectionStatePrompt";

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
  const [actionMsg, setActionMsg] = useState<{ ok: boolean; text: string } | null>(
    null,
  );

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
    <div className="space-y-2 rounded-md border bg-muted/10 px-3 py-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <KeyRound className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            OAuth status
          </span>
          <Badge variant="muted" className="rounded font-mono text-[11px]">
            authorization_code
          </Badge>
        </div>
        <div className="flex gap-1">
          <Button
            type="button"
            variant={needsReauth ? "default" : "outline"}
            size="xs"
            onClick={handleConnect}
            disabled={startOAuth.isPending}
          >
            <ExternalLink />
            {needsReauth ? "Connect" : "Reconnect"}
          </Button>
          {tokenAcquired && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={handleReacquire}
              disabled={reacquire.isPending}
            >
              <RefreshCw className={cn(reacquire.isPending && "animate-spin")} />
              {reacquire.isPending ? "Refreshing..." : "Refresh now"}
            </Button>
          )}
        </div>
      </div>

      {isLoading && (
        <div className="text-xs text-muted-foreground">Loading OAuth status…</div>
      )}

      {error && (
        <Alert variant="destructive" className="px-2 py-1">
          <AlertDescription className="text-xs">
            <span className="font-medium">Status unavailable:</span>{" "}
            {formatActionError(error, "fetch failed")}
          </AlertDescription>
        </Alert>
      )}

      {status?.needs_reauth && <ConnectionStatePrompt status={status} />}

      {status && <StatusGrid status={status} />}

      {status?.authenticated_by && (
        <div className="text-xs text-muted-foreground">
          Authorized by <span className="font-mono">{status.authenticated_by}</span>
          {status.authenticated_at && <> {formatRelative(status.authenticated_at)}</>}
        </div>
      )}

      {status?.last_error && status.last_error.trim() !== "" && (
        <Alert variant="destructive" className="px-2 py-1">
          <AlertDescription className="text-xs">
            <span className="font-medium">Last error:</span> {status.last_error}
          </AlertDescription>
        </Alert>
      )}

      {actionMsg && actionMsg.text.trim() !== "" && (
        <Alert
          variant={actionMsg.ok ? "success" : "destructive"}
          className="px-2 py-1"
        >
          <AlertDescription className="text-xs">{actionMsg.text}</AlertDescription>
        </Alert>
      )}

      <AuthEventHistory kind={kind} name={name} />
    </div>
  );
}

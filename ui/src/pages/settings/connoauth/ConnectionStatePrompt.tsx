import { AlertTriangle } from "lucide-react";
import type { ConnectionOAuthStatus } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { formatRelative } from "./format";

// ConnectionStatePrompt renders the appropriate explainer band for the three
// distinct needs-reauth states. Distinguishing them is the difference between
// an operator panicking ("the token vanished!") and an operator nodding ("ah,
// the IdP rejected the refresh, I need to reconnect"). See issue #395 Part 3.
export function ConnectionStatePrompt({ status }: { status: ConnectionOAuthStatus }) {
  if (status.last_revocation) {
    return <RevocationPrompt revocation={status.last_revocation} />;
  }
  if (status.token_acquired) {
    return (
      <Alert variant="warning" className="px-2 py-1.5">
        <AlertDescription className="text-xs">
          <span className="font-medium">Reauth needed soon.</span> The current access
          token still works, but the refresh-token deadline has passed. Click{" "}
          <strong>Connect</strong> to issue a fresh credential.
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <Alert variant="warning" className="px-2 py-1.5">
      <AlertDescription className="text-xs">
        <span className="font-medium">Not connected.</span> Click{" "}
        <strong>Connect</strong> to authorize this connection in your browser. The
        platform will then keep the access token refreshed automatically — including
        for cron jobs and scheduled prompts — until the upstream invalidates the
        refresh token.
      </AlertDescription>
    </Alert>
  );
}

// RevocationPrompt renders the explainer for a connection whose last known
// state is "token deleted by the platform." The reason field tells us how the
// verdict was reached, and the wording differs substantially:
//
//   - refresh_expired: NO IdP call happened. The previous successful refresh
//     response disclosed a hard deadline via refresh_expires_in (e.g.,
//     Keycloak SsoSessionMaxLifespan), the deadline arrived, and the platform
//     stopped before contacting the IdP. Saying the IdP "returned
//     refresh_expired" is wrong — the IdP wasn't asked.
//
//   - invalid_grant: the IdP was called and returned RFC 6749 §5.2
//     invalid_grant. The session is genuinely terminated upstream (operator
//     revoked consent, replay protection fired, etc.).
//
//   - no_refresh_token: there was no refresh token to exchange. Always a local
//     determination — never reached the IdP.
//
// Each case gets its own headline and explanation so operators can tell
// whether the IdP rejected something or the platform respected a deadline.
function RevocationPrompt({
  revocation,
}: {
  revocation: NonNullable<ConnectionOAuthStatus["last_revocation"]>;
}) {
  const reason = revocation.reason;
  const host = revocation.idp_host;
  const occurred = formatRelative(revocation.occurred_at);
  return (
    <Alert variant="destructive" className="px-2 py-1.5">
      <AlertTriangle />
      <AlertDescription className="text-xs">
        <span>
          <span className="font-medium">{revocationHeadline(reason)}</span>{" "}
          <RevocationBody reason={reason} host={host} />{" "}
          <span className="text-muted-foreground">({occurred})</span> Click{" "}
          <strong>Connect</strong> to re-authorize.
        </span>
      </AlertDescription>
    </Alert>
  );
}

// revocationHeadline returns the short, bold lead text for each revocation
// reason. Exported so the test file can assert exact wording without
// rendering.
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
  const idp = host ? <span className="font-mono">{host}</span> : <>the upstream IdP</>;
  switch (reason) {
    case "refresh_expired":
      return (
        <>
          The previous successful refresh from {idp} disclosed this deadline
          (typically the IdP's session-lifetime ceiling), so the platform did not
          attempt another refresh.
        </>
      );
    case "invalid_grant":
      return (
        <>
          {idp} returned <span className="font-mono">invalid_grant</span>. Common
          causes: the operator revoked consent, the IdP detected replay of a rotated
          single-use refresh token, or the session was administratively terminated.
        </>
      );
    case "no_refresh_token":
      return <>The platform had no refresh token to exchange with {idp}.</>;
    default:
      return <>{idp} could not extend the session.</>;
  }
}

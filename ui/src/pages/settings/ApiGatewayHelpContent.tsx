import { type ReactNode } from "react";

// ApiGatewayHelpContent owns the long-form prose for the API gateway
// connection editor's help modals. Kept separate from
// ConnectionsPanel.tsx so the editor form stays scannable and so the
// reference content can be reviewed (and translated, if that's ever
// needed) without diffing the editor's behavior.

export function ApiGatewayAuthHelp() {
  return (
    <div className="space-y-4">
      <p>
        The connection's <code>auth_mode</code> selects what credential
        the gateway attaches to every outbound request. Pick the one
        the upstream API expects. mTLS is independent and can be
        configured alongside any other mode.
      </p>
      <table className="w-full border-collapse">
        <thead>
          <tr className="border-b text-left text-xs uppercase tracking-wider text-muted-foreground">
            <th className="w-1/3 py-2 pr-4 font-medium">Mode</th>
            <th className="py-2 font-medium">What gets sent</th>
          </tr>
        </thead>
        <tbody className="text-sm">
          <Row label="None" mode="none" sends="No outbound auth header.">
            Public APIs or upstreams where the platform sits behind a
            trusted-network boundary that authenticates separately.
          </Row>
          <Row label="Bearer token" mode="bearer" sends="Authorization: Bearer <credential>">
            Long-lived API tokens (GitHub PAT, Stripe secret key, etc.).
            The credential is stored encrypted and never returned in
            admin responses.
          </Row>
          <Row label="API key" mode="api_key" sends="<header>: <credential>  or  ?<param>=<credential>">
            Vendor APIs that use a custom header
            (<code>X-API-Key</code>, <code>Api-Token</code>) or a query
            parameter (<code>?api_key=</code>). Configure the header
            name or query parameter under the dropdown.
          </Row>
          <Row label="Basic (RFC 7617)" mode="basic" sends="Authorization: Basic base64(user:password)">
            RFC 7617 Basic auth, for legacy APIs that never moved to
            bearer or OAuth (Jenkins, on-prem Jira / Confluence Server,
            old internal apps). Password may be empty for the legacy
            <code> token:</code> pattern.
          </Row>
          <Row label="OAuth 2.1, client credentials" mode="oauth2_client_credentials" sends="Authorization: Bearer <token>">
            Machine-to-machine OAuth 2.1. The platform exchanges
            client_id + client_secret at the token endpoint and refreshes
            transparently. No human in the loop.
          </Row>
          <Row label="OAuth 2.1, authorization code" mode="oauth2_authorization_code" sends="Authorization: Bearer <token>">
            User-driven OAuth 2.1 with persisted refresh tokens. The
            admin completes a one-time browser sign-in via Connect; the
            refresh token is stored encrypted and the gateway refreshes
            access tokens silently for all subsequent calls.
          </Row>
          <Row label="mTLS (client certificate)" mode="mtls" sends="(no header, client certificate at TLS handshake)">
            The X.509 client certificate IS the credential, presented
            during the TLS handshake (RFC 5246 / 8446). For upstreams
            that map the cert's subject DN to a user identity: service
            mesh peers, PKI-fronted internal APIs, healthcare
            integration engines, FedRAMP services, Vault cert auth,
            Kubernetes API server, etc.
          </Row>
        </tbody>
      </table>
      <p className="text-xs text-muted-foreground">
        Need both a bearer token AND a per-vendor header (Google's
        <code className="mx-1">x-goog-user-project</code>, a vendor
        subscription key)? Use the static headers section below the
        auth fields: it adds operator-supplied headers to every call in
        addition to whatever the auth mode contributes.
      </p>
    </div>
  );
}

function Row({
  label,
  mode,
  sends,
  children,
}: {
  label: string;
  mode: string;
  sends: string;
  children: ReactNode;
}) {
  return (
    <tr className="border-b align-top last:border-0">
      <td className="py-3 pr-4">
        <div className="font-medium">{label}</div>
        <code className="mt-0.5 block break-all text-[11px] text-muted-foreground">
          {mode}
        </code>
      </td>
      <td className="py-3">
        <div className="font-mono text-xs text-muted-foreground">{sends}</div>
        <div className="mt-1">{children}</div>
      </td>
    </tr>
  );
}

export function ApiGatewayTLSHelp() {
  return (
    <div className="space-y-4">
      <p>
        Two independent TLS concerns on every <code>kind: api</code>{" "}
        connection. Both are optional. Set whatever the upstream
        actually requires.
      </p>

      <Section title="Client certificate (mtls_client_cert_pem + mtls_client_key_pem)">
        <p>
          The X.509 client certificate the gateway presents during the
          TLS handshake. Required when the upstream's TLS server is
          configured to demand a client cert. With{" "}
          <code>auth_mode: mtls</code>, the cert IS the credential.
          With any other auth mode, the cert is layered on top (bearer
          + mTLS, oauth2 + mTLS, etc.).
        </p>
        <ul className="ml-5 list-disc space-y-1 text-xs">
          <li>Cert and key are mutually required: set both, or neither.</li>
          <li>The key must match the cert (signature check at save time).</li>
          <li>
            Key strength: RSA at least 2048 bits, ECDSA P-256 / P-384 /
            P-521, or Ed25519. Weaker keys are rejected.
          </li>
          <li>
            The private key is stored encrypted and shown back as{" "}
            <code>[REDACTED]</code>; paste the new key to rotate.
          </li>
        </ul>
      </Section>

      <Section title="CA bundle (tls_ca_bundle_pem)">
        <p>
          A PEM bundle of root certificates added to the trust store
          when verifying the upstream's TLS certificate. Required when
          the upstream is signed by a private CA (corporate root,
          cluster-internal CA, mesh CA) that public-CA chains do not
          cover. Public CAs remain trusted: the bundle is appended, not
          substituted.
        </p>
        <ul className="ml-5 list-disc space-y-1 text-xs">
          <li>
            Bundle must contain at least one parseable{" "}
            <code>CERTIFICATE</code> block.
          </li>
          <li>
            For OAuth modes, the same bundle is also used when calling
            the IdP token endpoint, so IdPs behind a private CA work
            end-to-end.
          </li>
          <li>
            There is no <code>insecure_skip_verify</code> toggle. A
            self-signed endpoint needs its CA pasted here.
          </li>
        </ul>
      </Section>

      <Section title="Cert expiry">
        <p>
          The leaf certificate's expiry surfaces in this editor as a
          color-coded badge: green at 30 or more days remaining, amber
          under 30 days, red when expired. The badge is informational:
          the gateway does not refuse to make calls with an expired
          cert. The upstream's TLS layer will reject the handshake on
          its own, and the resulting error reaches the model through
          the normal feedback loop.
        </p>
      </Section>
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <h3 className="mb-1 text-sm font-semibold">{title}</h3>
      <div className="space-y-2 text-sm">{children}</div>
    </div>
  );
}

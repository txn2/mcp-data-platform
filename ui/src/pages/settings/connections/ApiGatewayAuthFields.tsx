import { useCallback, useState } from "react";
import { AlertCircle } from "lucide-react";

import { useStartConnectionOAuth } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  update,
  type ConfigFormProps,
} from "./fields";
import { OAuthFields } from "./OAuthFields";
import { AUTH_MODE_OAUTH } from "./oauthVocabulary";
import { SignedJWTAuthFields } from "./SignedJWTAuthFields";

// The auth half of an HTTP-based connection editor: the mode picker and the
// credential fields each mode needs. Split from ApiGatewayConfigForm so the
// form file states the connection's shape and this one states its auth; the
// graphql form renders it too, because both kinds read their credentials
// through internal/upstreamauth.

const AUTH_MODES = [
  { value: "none", label: "None" },
  { value: "bearer", label: "Bearer token" },
  { value: "api_key", label: "API key" },
  { value: "basic", label: "Basic (RFC 7617)" },
  { value: "signed_jwt", label: "Signed JWT (the platform mints the token)" },
  { value: AUTH_MODE_OAUTH, label: "OAuth 2.1" },
  { value: "mtls", label: "mTLS (client certificate is the credential)" },
];

const API_KEY_PLACEMENTS = [
  { value: "header", label: "Header" },
  { value: "query", label: "Query string" },
];

function ApiKeyFields({ config, onChange }: ConfigFormProps) {
  return (
    <ConfigGroup title="API key">
      <ConfigField
        label="Credential"
        help="The API key value. Encrypted at rest. Use [REDACTED] to keep an existing value when re-saving."
        value={String(config.credential ?? "")}
        onChange={(v) => onChange(update(config, "credential", v))}
        sensitive
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigSelect
          label="Placement"
          value={String(config.api_key_placement ?? "header")}
          onChange={(v) => onChange(update(config, "api_key_placement", v))}
          options={API_KEY_PLACEMENTS}
        />
        {config.api_key_placement === "query" ? (
          <ConfigField
            label="Query parameter name"
            help="e.g. api_key, apikey, key."
            value={String(config.api_key_param ?? "")}
            onChange={(v) => onChange(update(config, "api_key_param", v))}
            placeholder="api_key"
            mono
          />
        ) : (
          <ConfigField
            label="Header name"
            help="Defaults to X-API-Key."
            value={String(config.api_key_header ?? "")}
            onChange={(v) => onChange(update(config, "api_key_header", v))}
            placeholder="X-API-Key"
            mono
          />
        )}
      </div>
    </ConfigGroup>
  );
}

function BasicAuthFields({ config, onChange }: ConfigFormProps) {
  return (
    <ConfigGroup title="HTTP Basic (RFC 7617)">
      <ConfigField
        label="Username"
        help="The userid. May contain any character except ':' (RFC 7617 §2)."
        value={String(config.username ?? "")}
        onChange={(v) => onChange(update(config, "username", v))}
        mono
      />
      <ConfigField
        label="Password"
        help="Encrypted at rest. Use [REDACTED] when re-saving without changing it. May be empty for legacy 'token in userid' patterns."
        value={String(config.password ?? "")}
        onChange={(v) => onChange(update(config, "password", v))}
        sensitive
      />
    </ConfigGroup>
  );
}

// ConnectPanel is the browser sign-in affordance for authorization_code. The
// Connect button needs a saved connection (the IdP redirect resolves the
// connection by kind AND name), so it states that requirement next to the
// disabled button rather than failing after the click.
//
// The kind is the editor's, not a constant: this block serves every
// HTTP-based kind, and starting the flow on the api kind for a graphql
// connection resolves a connection that does not exist.
function ConnectPanel({
  kind,
  connectionName,
  isCreate,
}: {
  kind: string;
  connectionName: string;
  isCreate: boolean;
}) {
  const startOAuth = useStartConnectionOAuth(kind);
  const [oauthError, setOAuthError] = useState<string | null>(null);
  const handleConnect = useCallback(() => {
    setOAuthError(null);
    if (!connectionName) {
      setOAuthError("Save the connection first, then click Connect.");
      return;
    }
    startOAuth.mutate(
      { name: connectionName, returnURL: window.location.pathname },
      {
        onSuccess: (resp) => {
          // Open the IdP authorization URL in a new tab so the
          // editor's unsaved fields survive the round-trip; the
          // callback handler redirects the new tab back to the
          // portal after persisting tokens.
          window.open(resp.authorization_url, "_blank", "noopener,noreferrer");
        },
        onError: (err) => {
          setOAuthError(err instanceof Error ? err.message : "Connect failed");
        },
      },
    );
  }, [connectionName, startOAuth]);

  return (
    <div className="space-y-2 rounded-md border bg-muted px-3 py-3">
      <p className="text-xs">
        <strong>Connect</strong> opens the IdP sign-in page in a new tab. After
        the browser flow completes, the platform persists the refresh token
        (encrypted) so subsequent tool calls refresh access tokens silently.
      </p>
      <p className="text-xs text-muted-foreground">
        Save the connection first; Connect needs the connection registered
        before the IdP redirect can find it.
      </p>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={handleConnect}
        disabled={isCreate || startOAuth.isPending || !connectionName}
      >
        {startOAuth.isPending ? "Opening IdP…" : "Connect"}
      </Button>
      {oauthError && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{oauthError}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}

// ApiGatewayAuthFields renders the mode picker plus whichever credential
// block the selected mode needs. The mode set matches what the apigateway
// toolkit accepts (pkg/toolkits/apigateway/config.go); mtls carries no fields
// here because the certificate itself is the credential and lives in the TLS
// material editor.
//
// OAuth is one mode here, with the grant a field of its own. A connection
// stored in the legacy spelling (auth_mode "oauth2_authorization_code" and the
// oauth2_* keys) reaches this form already folded onto the canonical keys by
// useConnectionForm, so there is one vocabulary to render and one to write.
export function ApiGatewayAuthFields({
  config,
  onChange,
  kind,
  connectionName,
  isCreate,
  onOpenHelp,
}: ConfigFormProps & {
  kind: string;
  connectionName: string;
  isCreate: boolean;
  onOpenHelp: () => void;
}) {
  const mode = String(config.auth_mode ?? "none");
  return (
    <>
      <ConfigSelect
        label="Auth mode"
        value={mode}
        onChange={(v) => onChange(update(config, "auth_mode", v))}
        options={AUTH_MODES}
        action={
          <Button type="button" variant="link" size="xs" onClick={onOpenHelp}>
            Learn about auth modes
          </Button>
        }
      />
      {mode === "bearer" && (
        <ConfigField
          label="Credential"
          help="Bearer token. Encrypted at rest. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
      )}
      {mode === "api_key" && (
        <ApiKeyFields config={config} onChange={onChange} />
      )}
      {mode === "basic" && (
        <BasicAuthFields config={config} onChange={onChange} />
      )}
      {mode === "signed_jwt" && (
        <SignedJWTAuthFields config={config} onChange={onChange} />
      )}
      {mode === AUTH_MODE_OAUTH && (
        <OAuthFields
          config={config}
          onChange={onChange}
          endpointAuthStyle
          jwtBearer
          connect={
            <ConnectPanel
              kind={kind}
              connectionName={connectionName}
              isCreate={isCreate}
            />
          }
        />
      )}
    </>
  );
}

import { useState, useCallback } from "react";
import { useStartAPIGatewayOAuth } from "@/api/admin/hooks";
import { HelpDialog } from "@/components/HelpDialog";
import { ApiGatewayAuthHelp, ApiGatewayTLSHelp } from "../ApiGatewayHelpContent";
import {
  ConfigField,
  SensitiveKeyValueEditor,
  asStringMap,
  update,
  type ConfigFormProps,
} from "./fields";
import { TLSMaterialEditor } from "./TlsMaterialEditor";
import { APICatalogPicker, LegacyOpenAPISpecBanner } from "./ApiCatalogPicker";

// ApiGatewayConfigForm renders the editor for kind=api connections —
// the HTTP API gateway. Field shape matches the apigateway toolkit
// config (see pkg/toolkits/apigateway/config.go): base_url, optional
// openapi_spec, the same auth_mode set the toolkit accepts (none,
// bearer, api_key, basic, oauth2_client_credentials,
// oauth2_authorization_code), timeouts, max_response_bytes, and the
// OAuth Connect button when authorization_code is selected.
//
// The Connect button is wired to the admin /api-gateway/connections/
// {name}/oauth-start endpoint shipped in #381; clicking it opens the
// IdP authorization URL in a new tab so the operator completes the
// browser flow without losing the editor's unsaved state.
export function ApiGatewayConfigForm({
  config,
  onChange,
  connectionName,
  isCreate,
}: ConfigFormProps & { connectionName: string; isCreate: boolean }) {
  const startOAuth = useStartAPIGatewayOAuth();
  const [oauthError, setOAuthError] = useState<string | null>(null);
  const [authHelpOpen, setAuthHelpOpen] = useState(false);
  const [tlsHelpOpen, setTlsHelpOpen] = useState(false);
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
    <>
      <ConfigField
        label="Base URL"
        help="HTTPS URL of the upstream API (no trailing slash)."
        value={String(config.base_url ?? "")}
        onChange={(v) => onChange(update(config, "base_url", v))}
        placeholder="https://api.vendor.example.com"
        mono
        required
      />
      <div>
        <div className="mb-1 flex items-center justify-between">
          <label className="block text-xs font-medium">Auth mode</label>
          <button
            type="button"
            onClick={() => setAuthHelpOpen(true)}
            className="text-xs text-blue-600 hover:underline dark:text-blue-400"
          >
            Learn about auth modes
          </button>
        </div>
        <select
          value={String(config.auth_mode ?? "none")}
          onChange={(e) => onChange(update(config, "auth_mode", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="none">None</option>
          <option value="bearer">Bearer token</option>
          <option value="api_key">API key</option>
          <option value="basic">Basic (RFC 7617)</option>
          <option value="oauth2_client_credentials">OAuth 2.1 client_credentials</option>
          <option value="oauth2_authorization_code">OAuth 2.1 authorization_code (browser sign-in)</option>
          <option value="mtls">mTLS (client certificate is the credential)</option>
        </select>
      </div>

      {config.auth_mode === "bearer" && (
        <ConfigField
          label="Credential"
          help="Bearer token. Encrypted at rest. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
      )}

      {config.auth_mode === "api_key" && (
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            API key
          </div>
          <ConfigField
            label="Credential"
            help="The API key value. Encrypted at rest. Use [REDACTED] to keep an existing value when re-saving."
            value={String(config.credential ?? "")}
            onChange={(v) => onChange(update(config, "credential", v))}
            sensitive
          />
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="mb-1 block text-xs font-medium">Placement</label>
              <select
                value={String(config.api_key_placement ?? "header")}
                onChange={(e) => onChange(update(config, "api_key_placement", e.target.value))}
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              >
                <option value="header">Header</option>
                <option value="query">Query string</option>
              </select>
            </div>
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
        </div>
      )}

      {config.auth_mode === "basic" && (
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            HTTP Basic (RFC 7617)
          </div>
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
        </div>
      )}

      {(config.auth_mode === "oauth2_client_credentials" ||
        config.auth_mode === "oauth2_authorization_code") && (
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            OAuth 2.1 — {config.auth_mode === "oauth2_authorization_code" ? "authorization_code" : "client_credentials"}
          </div>
          <ConfigField
            label="Token URL"
            help="OAuth token endpoint."
            value={String(config.oauth2_token_url ?? "")}
            onChange={(v) => onChange(update(config, "oauth2_token_url", v))}
            placeholder="https://idp.example.com/oauth/token"
            mono
          />
          {config.auth_mode === "oauth2_authorization_code" && (
            <ConfigField
              label="Authorization URL"
              help="Where the browser is sent to sign in."
              value={String(config.oauth2_authorization_url ?? "")}
              onChange={(v) => onChange(update(config, "oauth2_authorization_url", v))}
              placeholder="https://idp.example.com/oauth/authorize"
              mono
            />
          )}
          <div className="grid grid-cols-2 gap-3">
            <ConfigField
              label="Client ID"
              value={String(config.oauth2_client_id ?? "")}
              onChange={(v) => onChange(update(config, "oauth2_client_id", v))}
              placeholder="platform-client"
              mono
            />
            <ConfigField
              label="Client Secret"
              help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
              value={String(config.oauth2_client_secret ?? "")}
              onChange={(v) => onChange(update(config, "oauth2_client_secret", v))}
              sensitive
            />
          </div>
          <ConfigField
            label="Scopes"
            help="Space-delimited scope string. Leave empty if the IdP does not require it."
            value={String(
              Array.isArray(config.oauth2_scopes)
                ? (config.oauth2_scopes as string[]).join(" ")
                : (config.oauth2_scopes ?? ""),
            )}
            onChange={(v) =>
              onChange(update(config, "oauth2_scopes", v.trim() ? v.split(/\s+/) : []))
            }
            placeholder="read:users write:orders"
            mono
          />
          <div>
            <label className="mb-1 block text-xs font-medium">Endpoint auth style</label>
            <select
              value={String(config.oauth2_endpoint_auth_style ?? "header")}
              onChange={(e) => onChange(update(config, "oauth2_endpoint_auth_style", e.target.value))}
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
            >
              <option value="header">Header (HTTP Basic) — OAuth 2.1 default</option>
              <option value="params">Form params — some IdPs require this</option>
            </select>
          </div>
          {config.auth_mode === "oauth2_authorization_code" && (
            <>
              <div>
                <label className="mb-1 block text-xs font-medium">OIDC prompt</label>
                <select
                  value={String(config.oauth2_prompt ?? "")}
                  onChange={(e) => onChange(update(config, "oauth2_prompt", e.target.value))}
                  className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
                >
                  <option value="">(default — no prompt parameter)</option>
                  <option value="login">login (force fresh credentials each Connect)</option>
                  <option value="consent">consent (force consent screen)</option>
                  <option value="select_account">select_account (force account picker)</option>
                  <option value="none">none (silent auth)</option>
                </select>
                <p className="mt-1 text-xs text-muted-foreground">
                  Leave default for non-OIDC OAuth providers that reject unknown parameters. Use <code>login</code> for Keycloak / Auth0 / Okta to defeat stale-form bugs by forcing a fresh credential prompt on every Connect.
                </p>
              </div>
              <div className="rounded-md border border-dashed bg-background px-3 py-3 space-y-2">
                <p className="text-xs">
                  <strong>Connect</strong> opens the IdP sign-in page in a new tab. After the
                  browser flow completes, the platform persists the refresh token (encrypted)
                  so subsequent tool calls refresh access tokens silently.
                </p>
                <p className="text-xs text-muted-foreground">
                  Save the connection first; Connect needs the connection registered before
                  the IdP redirect can find it.
                </p>
                <button
                  type="button"
                  onClick={handleConnect}
                  disabled={isCreate || startOAuth.isPending || !connectionName}
                  className="inline-flex items-center gap-1.5 rounded-md border bg-background px-3 py-1.5 text-xs font-medium hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {startOAuth.isPending ? "Opening IdP…" : "Connect"}
                </button>
                {oauthError && (
                  <p className="text-xs text-red-600 dark:text-red-400">{oauthError}</p>
                )}
              </div>
            </>
          )}
        </div>
      )}

      <TLSMaterialEditor
        config={config}
        onChange={onChange}
        onOpenHelp={() => setTlsHelpOpen(true)}
      />

      <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-2">
        <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Static headers
        </div>
        <p className="text-xs text-muted-foreground">
          Headers added to every outbound request, in addition to whatever
          Auth mode contributes. Required by APIs that demand both an
          OAuth bearer AND a separate key, e.g. Google's
          <code className="mx-1">x-goog-user-project</code> for quota
          billing or a vendor subscription header. Values are encrypted
          at rest; existing values are masked.
        </p>
        <SensitiveKeyValueEditor
          entries={asStringMap(config.static_headers)}
          onChange={(next) =>
            onChange(
              update(
                config,
                "static_headers",
                Object.keys(next).length === 0 ? undefined : next,
              ),
            )
          }
          keyPlaceholder="X-Goog-User-Project"
          valuePlaceholder="header value"
        />
      </div>

      <APICatalogPicker config={config} onChange={onChange} />
      <LegacyOpenAPISpecBanner config={config} onChange={onChange} />


      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Connect timeout"
          help="Initial dial timeout (e.g. 10s, 1m)."
          value={String(config.connect_timeout ?? "")}
          onChange={(v) => onChange(update(config, "connect_timeout", v))}
          placeholder="10s"
          mono
        />
        <ConfigField
          label="Call timeout"
          help="Per-call upstream timeout (e.g. 60s)."
          value={String(config.call_timeout ?? "")}
          onChange={(v) => onChange(update(config, "call_timeout", v))}
          placeholder="60s"
          mono
        />
      </div>

      <ConfigField
        label="Max response bytes"
        help="Cap on response body size returned through api_invoke_endpoint. Above this, the call sets body_truncated=true and hints the model toward api_export. Default 10485760 (10 MiB)."
        type="number"
        value={String(config.max_response_bytes ?? "")}
        onChange={(v) => onChange(update(config, "max_response_bytes", v ? Number(v) : undefined))}
        placeholder="10485760"
      />

      <div>
        <label className="mb-1 block text-xs font-medium">Trust level</label>
        <select
          value={String(config.trust_level ?? "untrusted")}
          onChange={(e) => onChange(update(config, "trust_level", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="untrusted">Untrusted (default)</option>
          <option value="trusted">Trusted</option>
        </select>
        <p className="mt-1 text-xs text-muted-foreground">
          Reserved for future content-fencing of upstream responses.
        </p>
      </div>

      <HelpDialog
        open={authHelpOpen}
        onOpenChange={setAuthHelpOpen}
        title="Authentication modes"
      >
        <ApiGatewayAuthHelp />
      </HelpDialog>

      <HelpDialog
        open={tlsHelpOpen}
        onOpenChange={setTlsHelpOpen}
        title="TLS and mTLS"
      >
        <ApiGatewayTLSHelp />
      </HelpDialog>
    </>
  );
}

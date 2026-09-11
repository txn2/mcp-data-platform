import {
  ConfigField,
  ConfigSelect,
  update,
  type ConfigFormProps,
} from "./fields";
import { OAuthFields } from "./OAuthFields";
import { AUTH_MODE_OAUTH } from "./oauthVocabulary";

const AUTH_MODES = [
  { value: "none", label: "None" },
  { value: "bearer", label: "Bearer token" },
  { value: "api_key", label: "API key" },
  { value: AUTH_MODE_OAUTH, label: "OAuth 2.1" },
];

const TRUST_LEVELS = [
  { value: "untrusted", label: "Untrusted (default)" },
  { value: "trusted", label: "Trusted" },
];

// Editor form for kind=mcp connections — the MCP gateway toolkit that proxies
// tools from an upstream MCP server. Field shape matches pkg/toolkits/gateway.
export function GatewayConfigForm({ config, onChange }: ConfigFormProps) {
  return (
    <>
      <ConfigField
        label="Endpoint"
        help="HTTPS URL of the upstream MCP server (Streamable HTTP transport)."
        value={String(config.endpoint ?? "")}
        onChange={(v) => onChange(update(config, "endpoint", v))}
        placeholder="https://vendor.example.com/mcp"
        mono
        required
      />
      <ConfigSelect
        label="Auth mode"
        value={String(config.auth_mode ?? "none")}
        onChange={(v) => onChange(update(config, "auth_mode", v))}
        options={AUTH_MODES}
        help="Bearer sends Authorization header; API key sends X-API-Key; OAuth obtains a managed bearer token via client_credentials or authorization_code+PKCE."
      />
      {(config.auth_mode === "bearer" || config.auth_mode === "api_key") && (
        <ConfigField
          label="Credential"
          help="Encrypted at rest. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
      )}
      {config.auth_mode === AUTH_MODE_OAUTH && (
        <OAuthFields config={config} onChange={onChange} />
      )}
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Connect timeout"
          help="Initial dial + tool discovery (e.g. 10s, 1m)."
          value={String(config.connect_timeout ?? "")}
          onChange={(v) => onChange(update(config, "connect_timeout", v))}
          placeholder="10s"
          mono
        />
        <ConfigField
          label="Call timeout"
          help="Per-tool-call upstream timeout (e.g. 60s)."
          value={String(config.call_timeout ?? "")}
          onChange={(v) => onChange(update(config, "call_timeout", v))}
          placeholder="60s"
          mono
        />
      </div>
      <ConfigSelect
        label="Trust level"
        value={String(config.trust_level ?? "untrusted")}
        onChange={(v) => onChange(update(config, "trust_level", v))}
        options={TRUST_LEVELS}
        help={`Reserved for future content-fencing of upstream responses. Leave at "untrusted" unless you control the upstream.`}
      />
    </>
  );
}

// Constants shared across the connection editor/viewer surfaces. Extracted
// from ConnectionsPanel.tsx (#766) so the panel, editor, and viewer can each
// import only what they render.

// Connection kinds the create form offers. Kind is immutable after creation.
export const AVAILABLE_KINDS = ["trino", "s3", "mcp", "api"];

// Kind badge colors for the list and viewer header.
const KIND_COLORS: Record<string, string> = {
  trino: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  datahub: "bg-purple-100 text-purple-800 dark:bg-purple-900/30 dark:text-purple-400",
  s3: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  mcp: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
  api: "bg-indigo-100 text-indigo-800 dark:bg-indigo-900/30 dark:text-indigo-400",
};

export function kindColor(kind: string): string {
  return KIND_COLORS[kind] ?? "bg-muted text-muted-foreground";
}

// Human labels for raw config keys, keyed by kind, used by the read-only
// viewer's Configuration table.
export const CONFIG_LABELS: Record<string, Record<string, string>> = {
  trino: {
    host: "Host",
    port: "Port",
    user: "Username",
    password: "Password",
    catalog: "Default Catalog",
    schema: "Default Schema",
    ssl: "SSL / TLS",
    ssl_verify: "SSL Verify",
    read_only: "Read Only",
    default_limit: "Default Limit",
    max_limit: "Max Limit",
    timeout: "Timeout",
    connection_name: "Connection Name",
    description: "Description",
  },
  s3: {
    endpoint: "Endpoint",
    region: "Region",
    access_key_id: "Access Key ID",
    secret_access_key: "Secret Access Key",
    session_token: "Session Token",
    profile: "Profile",
    bucket_prefix: "Bucket Prefix",
    use_path_style: "Force Path Style",
    disable_ssl: "Disable SSL",
    read_only: "Read Only",
    timeout: "Timeout",
    max_get_size: "Max GET Size",
    max_put_size: "Max PUT Size",
    connection_name: "Connection Name",
  },
  mcp: {
    endpoint: "Endpoint",
    auth_mode: "Auth Mode",
    credential: "Credential",
    connect_timeout: "Connect Timeout",
    call_timeout: "Call Timeout",
    trust_level: "Trust Level",
    connection_name: "Connection Name",
    oauth_grant: "OAuth Grant Type",
    oauth_token_url: "OAuth Token URL",
    oauth_authorization_url: "OAuth Authorization URL",
    oauth_client_id: "OAuth Client ID",
    oauth_client_secret: "OAuth Client Secret",
    oauth_scope: "OAuth Scope",
    oauth_prompt: "OAuth Prompt",
  },
  api: {
    base_url: "Base URL",
    auth_mode: "Auth Mode",
    credential: "Credential",
    api_key_header: "API Key Header",
    api_key_param: "API Key Parameter",
    api_key_placement: "API Key Placement",
    username: "Username",
    password: "Password",
    connect_timeout: "Connect Timeout",
    call_timeout: "Call Timeout",
    trust_level: "Trust Level",
    max_response_bytes: "Max Response Bytes",
    max_inline_bytes: "Max Inline Bytes",
    catalog_id: "OpenAPI Catalog",
    connection_name: "Connection Name",
    oauth2_token_url: "OAuth2 Token URL",
    oauth2_authorization_url: "OAuth2 Authorization URL",
    oauth2_client_id: "OAuth2 Client ID",
    oauth2_client_secret: "OAuth2 Client Secret",
    oauth2_scopes: "OAuth2 Scopes",
    oauth2_endpoint_auth_style: "OAuth2 Auth Style",
    oauth2_prompt: "OAuth2 Prompt",
    mtls_client_cert_pem: "mTLS Client Certificate",
    mtls_client_key_pem: "mTLS Client Key",
    mtls_cert_not_after: "mTLS Cert Expiry",
    tls_ca_bundle_pem: "TLS CA Bundle",
    static_headers: "Static Headers",
  },
};

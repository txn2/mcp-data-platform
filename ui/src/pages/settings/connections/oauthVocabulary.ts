// The OAuth connection-config vocabulary, as the browser needs it.
//
// A connection's OAuth configuration has two spellings. The canonical one
// (`auth_mode: "oauth"` + `oauth_grant` + `oauth_*` keys, with the scope a
// space-delimited string) is what every kind reads first and what the admin
// API persists. The legacy one (`auth_mode: "oauth2_authorization_code"` +
// `oauth2_*` keys, with the scopes an array) is what the api-gateway editor
// used to write; the server still reads it as a fallback so a connection
// authored before the two unified keeps working.
//
// The editor speaks only the canonical spelling. It folds a legacy config onto
// the canonical keys when it loads one, so the fields show the values the
// platform is actually authenticating with, and a save writes one vocabulary
// rather than stacking the other one beside it (#1681, #1682).
//
// The Go counterpart is pkg/connoauth/canonical.go and the two are held
// together by TestGoAndBrowserAgreeOnTheOAuthVocabulary. They differ on one
// point by design: a disagreeing pair is an error on the server, which refuses
// the write, while here the canonical value wins and the legacy key is dropped,
// because that is the value the connection is live on and the one the operator
// has to see in the field.

// OAUTH_LEGACY_PAIRS maps each legacy key to the canonical key it becomes,
// one entry per line so the Go parity test can read it.
export const OAUTH_LEGACY_PAIRS: { legacy: string; canonical: string }[] = [
  { legacy: "oauth2_token_url", canonical: "oauth_token_url" },
  { legacy: "oauth2_authorization_url", canonical: "oauth_authorization_url" },
  { legacy: "oauth2_client_id", canonical: "oauth_client_id" },
  { legacy: "oauth2_client_secret", canonical: "oauth_client_secret" },
  { legacy: "oauth2_scopes", canonical: "oauth_scope" },
  { legacy: "oauth2_prompt", canonical: "oauth_prompt" },
  {
    legacy: "oauth2_endpoint_auth_style",
    canonical: "oauth_endpoint_auth_style",
  },
];

// The canonical auth_mode for an OAuth connection. The flow lives in
// oauth_grant, so this one value covers both grants.
export const AUTH_MODE_OAUTH = "oauth";

// The legacy auth_mode values, which encoded the grant in the mode name, and
// the grant each one means.
export const LEGACY_OAUTH_AUTH_MODES: Record<string, string> = {
  oauth2_client_credentials: "client_credentials",
  oauth2_authorization_code: "authorization_code",
};

type Config = Record<string, unknown>;

// storedOAuthGrant reads the grant a stored connection config selects: the
// canonical oauth_grant, the grant under an mcp connection's nested "oauth"
// block (which the server flattens onto oauth_grant), or the grant a legacy
// auth_mode encoded. Empty when the config states none.
export function storedOAuthGrant(config: Config | undefined): string {
  if (!config) return "";
  return (
    stringValue(config.oauth_grant) ||
    stringValue(nestedBlock(config.oauth).grant) ||
    (LEGACY_OAUTH_AUTH_MODES[String(config.auth_mode ?? "")] ?? "")
  );
}

// stringValue is v when it is a string, and "" otherwise.
function stringValue(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// nestedBlock is v when it is a plain object, and an empty one otherwise.
function nestedBlock(v: unknown): Config {
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Config) : {};
}

// legacyScopeToCanonical turns the legacy scope array into the canonical
// space-delimited string. A value that is already a string is passed through:
// some externally-authored configs wrote one.
function legacyScopeToCanonical(raw: unknown): string {
  if (Array.isArray(raw)) return (raw as unknown[]).map(String).join(" ");
  return String(raw ?? "");
}

// canonicalizeOAuthConfig returns config with every legacy OAuth key folded
// onto its canonical sibling and dropped, and a legacy auth_mode rewritten to
// AUTH_MODE_OAUTH plus the grant it encoded. A canonical value already in
// place wins and is left alone: it is the one the platform authenticates with,
// so it is the one the operator has to be shown and to correct.
//
// Config keys outside the OAuth vocabulary are untouched.
export function canonicalizeOAuthConfig(config: Config): Config {
  const out: Config = { ...config };
  for (const { legacy, canonical } of OAUTH_LEGACY_PAIRS) {
    if (!(legacy in out)) continue;
    if (!(canonical in out)) {
      out[canonical] =
        legacy === "oauth2_scopes"
          ? legacyScopeToCanonical(out[legacy])
          : out[legacy];
    }
    delete out[legacy];
  }
  const grant = LEGACY_OAUTH_AUTH_MODES[String(out.auth_mode ?? "")];
  if (grant) {
    out.auth_mode = AUTH_MODE_OAUTH;
    if (!out.oauth_grant) out.oauth_grant = grant;
  }
  return out;
}

// shadowedOAuthKeys lists the legacy keys on config whose value nothing reads
// because the canonical sibling is set. The connection viewer marks these:
// a mixed config otherwise lists both sets as equals, which is how an
// operator's real client id sat beside a placeholder for an afternoon with
// nothing saying which one was live (#1682).
//
// auth_mode is included when it names a legacy grant and an explicit
// oauth_grant is also present, because the grant is read from oauth_grant.
export function shadowedOAuthKeys(config: Config): string[] {
  const shadowed: string[] = [];
  for (const { legacy, canonical } of OAUTH_LEGACY_PAIRS) {
    if (legacy in config && canonical in config) shadowed.push(legacy);
  }
  const mode = String(config.auth_mode ?? "");
  if (mode in LEGACY_OAUTH_AUTH_MODES && "oauth_grant" in config) {
    shadowed.push("auth_mode");
  }
  return shadowed;
}

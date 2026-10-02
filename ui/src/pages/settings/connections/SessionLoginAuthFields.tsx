import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  PEMTextarea,
  asStringMap,
  update,
  type ConfigFormProps,
} from "./fields";
import { KeyValueEditor } from "./keyvalue";

// The sign-in block of the api and graphql connection editors under
// auth_mode=session_login (#2015): the platform signs in with a stored
// credential, carries the token the sign-in returns on every call, and signs
// in again when the upstream rejects it. Tableau, MicroStrategy and Veeva
// Vault authenticate this way.

const METHODS = ["POST", "PUT", "PATCH", "GET", "DELETE"].map((m) => ({ value: m, label: m }));

const CONTENT_TYPES = [
  { value: "application/json", label: "application/json" },
  { value: "application/xml", label: "application/xml" },
  { value: "application/x-www-form-urlencoded", label: "application/x-www-form-urlencoded" },
];

// A map key is written only while it holds an entry, so clearing the last one
// removes the key rather than saving an empty object.
function setMap(config: Record<string, unknown>, key: string, next: Record<string, string>) {
  return update(config, key, Object.keys(next).length === 0 ? undefined : next);
}

// The statuses are read back as the list a saved connection holds or the
// comma-separated text an operator typed; the server reads both forms, so the
// text is saved as typed and a comma being typed is not taken away.
function statusesText(raw: unknown): string {
  if (Array.isArray(raw)) return raw.join(", ");
  return typeof raw === "string" ? raw : "";
}

export function SessionLoginAuthFields({ config, onChange }: ConfigFormProps) {
  const text = (key: string) => String(config[key] ?? "");
  const set = (key: string) => (v: string) => onChange(update(config, key, v));
  return (
    <ConfigGroup title="Session sign-in">
      <div className="grid grid-cols-[1fr_8rem] gap-3">
        <ConfigField
          label="Sign-in URL"
          help="Where the platform signs in: an absolute URL, or a path under the base URL, e.g. /api/3.22/auth/signin."
          value={text("session_login_url")}
          onChange={set("session_login_url")}
          placeholder="/api/3.22/auth/signin"
          mono
          required
        />
        <ConfigSelect
          label="Sign-in method"
          value={text("session_login_method") || "POST"}
          onChange={set("session_login_method")}
          options={METHODS}
        />
      </div>
      <PEMTextarea
        label="Sign-in body"
        help="The request body the sign-in sends, with {{secret}} where the secret goes. Not secret itself, so it reads back as written."
        value={text("session_login_body")}
        onChange={set("session_login_body")}
        placeholder={'{"credentials":{"personalAccessTokenName":"platform","personalAccessTokenSecret":"{{secret}}","site":{"contentUrl":"acme"}}}'}
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigSelect
          label="Body content type"
          value={text("session_login_content_type") || "application/json"}
          onChange={set("session_login_content_type")}
          options={CONTENT_TYPES}
        />
        <ConfigField
          label="Secret"
          help="Written into the body at {{secret}}: a personal access token secret, an API key or a password. Encrypted at rest. Use [REDACTED] to keep the existing value."
          value={text("session_login_secret")}
          onChange={set("session_login_secret")}
          sensitive
        />
      </div>
      <div className="space-y-1.5">
        <p className="text-xs font-medium">Sign-in headers</p>
        <p className="text-xs text-muted-foreground">
          Sent on the sign-in request only. The sign-in asks for JSON unless an Accept header here says otherwise.
        </p>
        <KeyValueEditor
          entries={asStringMap(config.session_login_headers)}
          onChange={(next) => onChange(setMap(config, "session_login_headers", next))}
          keyPlaceholder="Accept"
          valuePlaceholder="application/json"
        />
      </div>
      <ConfigField
        label="Token location"
        help="Where the token is in the sign-in response: body:<json path> or header:<name>."
        value={text("session_token_source")}
        onChange={set("session_token_source")}
        placeholder="body:credentials.token"
        mono
        required
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Token header"
          help="The header the token is sent in on every call. With this and the prefix both blank: Authorization: Bearer <token>."
          value={text("session_token_header")}
          onChange={set("session_token_header")}
          placeholder="X-Tableau-Auth"
          mono
        />
        <ConfigField
          label="Token prefix"
          help="Written before the token in its header, e.g. Bearer followed by a space."
          value={text("session_token_prefix")}
          onChange={set("session_token_prefix")}
          mono
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Session lifetime"
          help="Sign in again after this long without waiting to be rejected (e.g. 2h). Blank uses a session until the upstream rejects it."
          value={text("session_ttl")}
          onChange={set("session_ttl")}
          placeholder="2h"
          mono
        />
        <ConfigField
          label="Sign-out URL"
          help="Called with the token when the connection is removed, replaced or shut down, so sessions do not pile up at the upstream."
          value={text("session_logout_url")}
          onChange={set("session_logout_url")}
          placeholder="/api/3.22/auth/signout"
          mono
        />
      </div>
      <div className="space-y-1.5">
        <p className="text-xs font-medium">Captured values</p>
        <p className="text-xs text-muted-foreground">
          Further values the sign-in response carries, read like the token. A call writes {"{session.<name>}"} in its
          path to have the value put there, e.g. site_id from body:credentials.site.id for
          /api/3.22/sites/{"{session.site_id}"}/workbooks.
        </p>
        <KeyValueEditor
          entries={asStringMap(config.session_capture)}
          onChange={(next) => onChange(setMap(config, "session_capture", next))}
          keyPlaceholder="site_id"
          valuePlaceholder="body:credentials.site.id"
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Expired statuses"
          help="Statuses that mean the session is no longer accepted; the platform signs in again and retries the call once. Defaults to 401."
          value={statusesText(config.session_expired_statuses)}
          onChange={(v) => onChange(update(config, "session_expired_statuses", v.trim() === "" ? undefined : v))}
          placeholder="401"
          mono
        />
        <ConfigField
          label="Expired text"
          help="Text in a response body that means the session is no longer accepted, for an upstream that reports it with 200 or 403."
          value={text("session_expired_marker")}
          onChange={set("session_expired_marker")}
          placeholder="INVALID_SESSION_ID"
          mono
        />
      </div>
    </ConfigGroup>
  );
}

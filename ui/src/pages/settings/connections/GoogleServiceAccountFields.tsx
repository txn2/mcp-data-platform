import { useId, useRef, useState } from "react";
import { AlertCircle, X } from "lucide-react";

import { useSecrets } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  update,
  type ConfigFormProps,
} from "./fields";

// The Google service account block of the api and graphql connection editors
// (#2061). A Google service account is one JSON key file an administrator
// downloads from Google Cloud; the platform reads the signing key, its id, the
// account and the token endpoint out of it and exchanges a signed assertion
// for an access token (oauth_grant=jwt_bearer, scopes in the assertion). The
// operator supplies the file, or the name of a stored secret holding it, and
// the scopes, and a delegated user only under domain-wide delegation.
//
// It is not an auth_mode of its own on the wire: the server derives
// auth_mode=oauth and oauth_grant=jwt_bearer from the key, so this form writes
// the key and leaves those unset.

export const AUTH_MODE_GOOGLE = "google_service_account";

const KEY_JSON = "google_service_account_json";
const KEY_SECRET = "google_service_account_secret";
const KEY_IDENTITY = "google_service_account_identity";

// The keys the key file fills in. A connection switched to a Google service
// account drops them, along with the mode and grant, so a value left over from
// another mode cannot outrank the file (an explicit key wins on the server).
const DERIVED_KEYS = [
  "auth_mode",
  "oauth_grant",
  "oauth_token_url",
  "oauth_client_id",
  "oauth_client_secret",
  "oauth_scope_placement",
  "jwt_algorithm",
  "jwt_private_key_pem",
  "jwt_client_secret",
  "jwt_key_id",
  "jwt_issuer",
  "jwt_audience",
  "credential",
];

/** isGoogleServiceAccount reports whether a stored config authenticates as one. */
export function isGoogleServiceAccount(
  config: Record<string, unknown>,
): boolean {
  return KEY_JSON in config || KEY_SECRET in config;
}

/** toGoogleServiceAccount is the config a switch to this mode starts from. */
export function toGoogleServiceAccount(
  config: Record<string, unknown>,
): Record<string, unknown> {
  const next = { ...config };
  for (const key of DERIVED_KEYS) delete next[key];
  return next;
}

/** fromGoogleServiceAccount is the config a switch away from this mode starts from. */
export function fromGoogleServiceAccount(
  config: Record<string, unknown>,
): Record<string, unknown> {
  const next = { ...config };
  for (const key of [KEY_JSON, KEY_SECRET, KEY_IDENTITY]) delete next[key];
  return next;
}

interface Identity {
  client_email: string;
  project_id: string;
  private_key_id: string;
}

// readKeyFile checks a key file the way the server does and returns its
// identity, or the refusal. It never repeats a value from the file.
export function readKeyFile(text: string): Identity | string {
  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(text) as Record<string, unknown>;
  } catch {
    return "This is not a JSON key file.";
  }
  if (parsed.type !== "service_account") {
    return "This key file is not for a service account. Download a key for a service account, not an OAuth client.";
  }
  for (const field of ["private_key", "client_email", "token_uri"]) {
    if (typeof parsed[field] !== "string" || !String(parsed[field]).trim()) {
      return `This key file has no ${field}.`;
    }
  }
  return {
    client_email: String(parsed.client_email),
    project_id: String(parsed.project_id ?? ""),
    private_key_id: String(parsed.private_key_id ?? ""),
  };
}

function storedIdentity(config: Record<string, unknown>): Identity | null {
  const raw = config[KEY_IDENTITY];
  if (!raw || typeof raw !== "object") return null;
  const id = raw as Partial<Identity>;
  return id.client_email
    ? {
        client_email: id.client_email,
        project_id: id.project_id ?? "",
        private_key_id: id.private_key_id ?? "",
      }
    : null;
}

function IdentityRows({ identity }: { identity: Identity }) {
  const rows: [string, string][] = [
    ["Account", identity.client_email],
    ["Project", identity.project_id],
    ["Key ID", identity.private_key_id],
  ];
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="font-mono break-all">{value || "—"}</dd>
        </div>
      ))}
    </dl>
  );
}

function KeyFile({ config, onChange }: ConfigFormProps) {
  const input = useRef<HTMLInputElement>(null);
  const [uploaded, setUploaded] = useState<Identity | null>(null);
  const [error, setError] = useState<string | null>(null);
  const identity = uploaded ?? storedIdentity(config);

  const load = async (file: File) => {
    const text = await file.text();
    const read = readKeyFile(text);
    if (typeof read === "string") {
      setError(read);
      return;
    }
    setError(null);
    setUploaded(read);
    const next = update(config, KEY_JSON, text);
    delete next[KEY_SECRET];
    onChange(next);
  };

  return (
    <div className="space-y-2">
      <Label className="text-xs">Key file</Label>
      <input
        ref={input}
        type="file"
        accept=".json,application/json"
        className="hidden"
        aria-label="Key file"
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) void load(file);
          e.target.value = "";
        }}
      />
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => input.current?.click()}
      >
        Upload key file
      </Button>
      <p className="text-xs text-muted-foreground">
        The JSON key Google Cloud issues for the service account. Encrypted at
        rest; it is never shown again, only the account and key it belongs to.
      </p>
      {identity && <IdentityRows identity={identity} />}
      {error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}

function StoredSecret({ config, onChange }: ConfigFormProps) {
  const secrets = useSecrets();
  const names = (secrets.data?.secrets ?? []).map((s) => s.name);
  const current = String(config[KEY_SECRET] ?? "");
  const options = [...new Set([current, ...names].filter(Boolean))].map(
    (n) => ({ value: n, label: n }),
  );
  return (
    <ConfigSelect
      label="Stored secret"
      help="A secret under Admin > Secrets whose value is the key file. Its allowed connections must include this connection, and it must not be limited to personas: the token it mints serves everyone who uses the connection."
      value={current}
      onChange={(v) => {
        const next = update(config, KEY_SECRET, v);
        delete next[KEY_JSON];
        onChange(next);
      }}
      options={options}
    />
  );
}

function Scopes({ config, onChange }: ConfigFormProps) {
  const id = useId();
  const [draft, setDraft] = useState("");
  const scopes = String(config.oauth_scope ?? "")
    .split(/\s+/)
    .filter(Boolean);
  const write = (next: string[]) =>
    onChange(update(config, "oauth_scope", next.join(" ")));
  const add = () => {
    const added = draft.split(/\s+/).filter(Boolean);
    if (added.length === 0) return;
    write([...new Set([...scopes, ...added])]);
    setDraft("");
  };
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs">
        Scopes
      </Label>
      <ul className="space-y-1">
        {scopes.map((scope) => (
          <li key={scope} className="flex items-center gap-2 font-mono text-xs">
            <span className="break-all">{scope}</span>
            <Button
              type="button"
              variant="ghost"
              size="xs"
              aria-label={`Remove ${scope}`}
              onClick={() => write(scopes.filter((s) => s !== scope))}
            >
              <X />
            </Button>
          </li>
        ))}
      </ul>
      <div className="flex gap-2">
        <Input
          id={id}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          placeholder="https://www.googleapis.com/auth/..."
          className="font-mono"
        />
        <Button type="button" variant="outline" size="sm" onClick={add}>
          Add
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Each scope is a full URL, such as
        https://www.googleapis.com/auth/display-video. A connection asks for
        only the scopes it needs; what the account can reach is still decided
        inside each Google product.
      </p>
    </div>
  );
}

/** GoogleServiceAccountFields renders the key, the scopes and the delegated user. */
export function GoogleServiceAccountFields({
  config,
  onChange,
}: ConfigFormProps) {
  const [source, setSource] = useState<"file" | "secret">(
    KEY_SECRET in config ? "secret" : "file",
  );
  return (
    <ConfigGroup title="Google service account">
      <div
        role="radiogroup"
        aria-label="Key"
        className="flex items-center gap-2"
      >
        <span className="text-xs font-medium">Key</span>
        {(
          [
            ["file", "Key file"],
            ["secret", "Stored secret"],
          ] as const
        ).map(([value, label]) => (
          <Button
            key={value}
            type="button"
            role="radio"
            aria-checked={source === value}
            variant={source === value ? "secondary" : "ghost"}
            size="sm"
            onClick={() => setSource(value)}
          >
            {label}
          </Button>
        ))}
      </div>
      {source === "file" ? (
        <KeyFile config={config} onChange={onChange} />
      ) : (
        <StoredSecret config={config} onChange={onChange} />
      )}
      <Scopes config={config} onChange={onChange} />
      <ConfigField
        label="Delegated user"
        help="A Workspace user to act as under domain-wide delegation; leave empty to act as the service account."
        value={String(config.jwt_subject ?? "")}
        onChange={(v) => onChange(update(config, "jwt_subject", v))}
        placeholder="analyst@example.com"
        mono
      />
    </ConfigGroup>
  );
}

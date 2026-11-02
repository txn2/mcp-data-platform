import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  ConfigToggle,
  PEMTextarea,
  update,
  type ConfigFormProps,
} from "./fields";

// The TLS modes a postgres connection takes, as libpq names them (#2400).
const SSL_MODES = [
  { value: "verify-full", label: "verify-full (default)" },
  { value: "verify-ca", label: "verify-ca" },
  { value: "require", label: "require" },
  { value: "disable", label: "disable" },
];

// text reads a config value for a text field.
const text = (config: Record<string, unknown>, key: string) => String(config[key] ?? "");

// intOrEmpty is a number field's value as the config stores it.
const intOrEmpty = (v: string) => (v ? parseInt(v, 10) : "");

// PostgresTLSFields is the TLS material a postgres connection presents and
// trusts, shown unless sslmode is disable.
function PostgresTLSFields({ config, onChange }: ConfigFormProps) {
  return (
    <ConfigGroup title="TLS">
      <PEMTextarea
        label="CA bundle (PEM)"
        help="Certificates trusted beside the system roots when verifying the server."
        value={text(config, "tls_ca_bundle_pem")}
        onChange={(v) => onChange(update(config, "tls_ca_bundle_pem", v))}
        placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
      />
      <PEMTextarea
        label="Client certificate (PEM)"
        value={text(config, "mtls_client_cert_pem")}
        onChange={(v) => onChange(update(config, "mtls_client_cert_pem", v))}
        placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
      />
      <PEMTextarea
        label="Client private key (PEM)"
        help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
        value={text(config, "mtls_client_key_pem")}
        onChange={(v) => onChange(update(config, "mtls_client_key_pem", v))}
        placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
        sensitive
      />
    </ConfigGroup>
  );
}

// PostgresLimitFields are a connection's pool size, statement timeout and row
// cap.
function PostgresLimitFields({ config, onChange }: ConfigFormProps) {
  return (
    <div className="grid grid-cols-3 gap-4">
      <ConfigField
        label="Pool size"
        type="number"
        value={text(config, "max_conns")}
        onChange={(v) => onChange(update(config, "max_conns", intOrEmpty(v)))}
        placeholder="5"
        help="Connections per replica, at most 100"
      />
      <ConfigField
        label="Statement timeout"
        value={text(config, "statement_timeout")}
        onChange={(v) => onChange(update(config, "statement_timeout", v))}
        placeholder="30s"
        mono
        help="Longest one statement may run, at most 10m"
      />
      <ConfigField
        label="Row cap"
        type="number"
        value={text(config, "max_rows")}
        onChange={(v) => onChange(update(config, "max_rows", intOrEmpty(v)))}
        placeholder="1000"
        help="Most rows one result returns"
      />
    </div>
  );
}

// Editor form for kind=postgres connections. Field names match
// pkg/toolkits/postgres's config keys.
export function PostgresConfigForm({ config, onChange }: ConfigFormProps) {
  const sslmode = String(config.sslmode ?? "verify-full");
  return (
    <>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Host"
          value={text(config, "host")}
          onChange={(v) => onChange(update(config, "host", v))}
          placeholder="db.example.com"
          mono
          required
          help="PostgreSQL server host name or address"
        />
        <ConfigField
          label="Port"
          type="number"
          value={text(config, "port")}
          onChange={(v) => onChange(update(config, "port", intOrEmpty(v)))}
          placeholder="5432"
          help="Server port (default: 5432)"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Database"
          value={text(config, "database")}
          onChange={(v) => onChange(update(config, "database", v))}
          placeholder="orders"
          mono
          required
          help="The platform's own database is refused"
        />
        <ConfigField
          label="User"
          value={text(config, "user")}
          onChange={(v) => onChange(update(config, "user", v))}
          placeholder="orders_app"
          mono
          required
          help="Every call runs as this user; its grants are what the tools can do"
        />
      </div>
      <ConfigField
        label="Password"
        value={text(config, "password")}
        onChange={(v) => onChange(update(config, "password", v))}
        placeholder="••••••••"
        sensitive
        help="Leave blank to keep the existing password. To use a stored secret, write {{secret:<name>}}; it is read when the connection is saved or the platform starts."
      />
      <ConfigSelect
        label="SSL mode"
        value={sslmode}
        onChange={(v) => onChange(update(config, "sslmode", v))}
        options={SSL_MODES}
        help="verify-full checks the server's certificate and its name; require encrypts without checking either."
      />
      {sslmode !== "disable" && <PostgresTLSFields config={config} onChange={onChange} />}
      <PostgresLimitFields config={config} onChange={onChange} />
      <ConfigToggle
        label="Read Only"
        checked={!!config.read_only}
        onChange={(v) => onChange(update(config, "read_only", v))}
        help="Refuse postgres_execute on this connection, for every persona. postgres_query always runs in a read-only transaction."
      />
    </>
  );
}

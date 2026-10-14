import {
  ConfigField,
  ConfigToggle,
  update,
  type ConfigFormProps,
} from "./fields";

// Editor form for kind=trino connections. Field shape matches the mcp-trino
// toolkit config.
export function TrinoConfigForm({ config, onChange }: ConfigFormProps) {
  return (
    <>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Host"
          value={String(config.host ?? "")}
          onChange={(v) => onChange(update(config, "host", v))}
          placeholder="trino.example.com"
          mono
          required
          help="Trino coordinator hostname or IP address"
        />
        <ConfigField
          label="Port"
          type="number"
          value={String(config.port ?? "")}
          onChange={(v) => onChange(update(config, "port", v ? parseInt(v, 10) : ""))}
          placeholder="443"
          help="Trino coordinator port (default: 443 for SSL, 8080 for plain)"
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Username"
          value={String(config.user ?? "")}
          onChange={(v) => onChange(update(config, "user", v))}
          placeholder="platform_svc"
          help="Service account username for Trino authentication"
        />
        <ConfigField
          label="Password"
          value={String(config.password ?? "")}
          onChange={(v) => onChange(update(config, "password", v))}
          placeholder="••••••••"
          sensitive
          help="Leave blank to keep the existing password. To use a stored secret, write {{secret:<name>}}; it is read when the connection is saved or the platform starts."
        />
      </div>
      <div className="grid grid-cols-2 gap-4">
        <ConfigField
          label="Default Catalog"
          value={String(config.catalog ?? "")}
          onChange={(v) => onChange(update(config, "catalog", v))}
          placeholder="iceberg"
          mono
          help="Default Trino catalog for queries (e.g. iceberg, hive, memory)"
        />
        <ConfigField
          label="Default Schema"
          value={String(config.schema ?? "")}
          onChange={(v) => onChange(update(config, "schema", v))}
          placeholder="public"
          mono
          help="Default Trino schema within the catalog"
        />
      </div>
      <ConfigToggle
        label="SSL / TLS"
        checked={!!config.ssl}
        onChange={(v) => onChange(update(config, "ssl", v))}
        help="Connect using HTTPS. Required for production deployments."
      />
      <ConfigToggle
        label="Read Only"
        checked={!!config.read_only}
        onChange={(v) => onChange(update(config, "read_only", v))}
        help="Reject write SQL (INSERT, UPDATE, DELETE, CREATE, DROP) on this connection. Applies to this connection alone; other Trino connections are unaffected."
      />
    </>
  );
}

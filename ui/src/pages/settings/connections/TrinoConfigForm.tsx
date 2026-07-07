import {
  ConfigField,
  ConfigToggle,
  KeyValueEditor,
  update,
  type ConfigFormProps,
} from "./fields";

// Editor form for kind=trino connections. Field shape matches the mcp-trino
// toolkit config plus the platform's DataHub integration keys
// (datahub_source_name, catalog_mapping).
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
          help="Leave blank to keep existing password"
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
      <div className="border-t pt-4 mt-2">
        <p className="text-xs font-medium mb-3">DataHub Integration</p>
        <ConfigField
          label="DataHub Source Name"
          value={String(config.datahub_source_name ?? "")}
          onChange={(v) => onChange(update(config, "datahub_source_name", v))}
          placeholder="trino"
          mono
          help="The platform identifier in DataHub URNs for datasets accessible through this connection (e.g. trino, postgres, hive). Defaults to trino if not set."
        />
        <div className="mt-4">
          <label className="mb-1 block text-xs font-medium">Catalog Mapping</label>
          <p className="mb-2 text-xs text-muted-foreground">
            Maps this connection's catalog names to DataHub catalog names. For example, if this connection uses catalog "rdbms" but DataHub knows it as "postgres", add rdbms → postgres.
          </p>
          <KeyValueEditor
            entries={config.catalog_mapping as Record<string, string> ?? {}}
            onChange={(v) => onChange(update(config, "catalog_mapping", Object.keys(v).length > 0 ? v : ""))}
            keyPlaceholder="connection catalog"
            valuePlaceholder="datahub catalog"
          />
        </div>
      </div>
    </>
  );
}

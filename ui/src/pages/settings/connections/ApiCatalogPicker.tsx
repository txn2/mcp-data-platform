import { useAPICatalogs } from "@/api/admin/hooks";

// APICatalogPicker renders the dropdown that points an api-kind
// connection at one of the globally-owned API catalogs. The model
// resolves connection → catalog → specs at runtime, so changing
// the dropdown immediately changes the set of operations that
// api_list_endpoints exposes for this connection on the next
// reload.
export function APICatalogPicker({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const { data: catalogs, isLoading } = useAPICatalogs();
  const value = String(config.catalog_id ?? "");
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">OpenAPI Catalog</label>
      <select
        value={value}
        onChange={(e) =>
          onChange({
            ...config,
            catalog_id: e.target.value === "" ? undefined : e.target.value,
          })
        }
        className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">— No spec (model can still invoke explicit method+path) —</option>
        {(catalogs ?? []).map((c) => (
          <option key={c.id} value={c.id}>
            {c.display_name}
            {c.version ? ` (v${c.version})` : ""} — {c.spec_count} spec
            {c.spec_count === 1 ? "" : "s"}
          </option>
        ))}
      </select>
      <p className="mt-1 text-xs text-muted-foreground">
        Catalogs are managed under{" "}
        <a className="underline" href={`${(import.meta.env.BASE_URL || "/").replace(/\/$/, "")}/admin/api-catalogs`}>
          API Catalogs
        </a>
        . One catalog can back many connections. {isLoading && "Loading…"}
      </p>
    </div>
  );
}

// LegacyOpenAPISpecBanner surfaces a one-time migration hint when an
// older connection still has the deprecated `openapi_spec` JSONB key
// set. The toolkit no longer reads it; the operator should move the
// content into a catalog (or accept that the connection has no
// model-visible spec until they do).
export function LegacyOpenAPISpecBanner({
  config,
  onChange,
}: {
  config: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const legacy = typeof config.openapi_spec === "string" ? (config.openapi_spec as string).trim() : "";
  if (!legacy) return null;
  if (config.catalog_id) {
    // Operator already wired a catalog — offer to clear the stale field.
    return (
      <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200">
        This connection still carries a deprecated inline <code>openapi_spec</code> field.
        It is no longer read by the toolkit; the catalog above is what the model sees.
        <button
          type="button"
          onClick={() => {
            const next = { ...config };
            delete next.openapi_spec;
            onChange(next);
          }}
          className="ml-2 underline"
        >
          Clear it
        </button>
      </div>
    );
  }
  return (
    <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200">
      This connection uses an inline OpenAPI spec which is no longer supported.
      Create a catalog under{" "}
      <a className="underline" href={`${(import.meta.env.BASE_URL || "/").replace(/\/$/, "")}/admin/api-catalogs`}>
        API Catalogs
      </a>{" "}
      and select it above. Until you do, <code>api_list_endpoints</code> returns no operations.
    </div>
  );
}

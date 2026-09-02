import { AlertTriangle } from "lucide-react";

import { useAPICatalogs } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { ConfigSelect } from "./fields";

// The catalogs admin page, reached from both surfaces below.
function catalogsHref(): string {
  return `${(import.meta.env.BASE_URL || "/").replace(/\/$/, "")}/admin/api-catalogs`;
}

// APICatalogPicker renders the dropdown that points an api-kind
// connection at one of the globally-owned API catalogs. The model
// resolves connection → catalog → specs at runtime, so changing
// the dropdown immediately changes the set of operations that
// api_discover exposes for this connection on the next
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
  const options = [
    {
      value: "",
      label: "— No spec (model can still invoke explicit method+path) —",
    },
    ...(catalogs ?? []).map((c) => ({
      value: c.id,
      label: `${c.display_name}${c.version ? ` (v${c.version})` : ""} — ${c.spec_count} spec${
        c.spec_count === 1 ? "" : "s"
      }`,
    })),
  ];
  // A listbox shows nothing for a value it has no item for, so while the
  // catalog list is still loading (or if the referenced catalog was deleted)
  // the stored id travels as its own option. Otherwise the field reads as
  // "no catalog" on a connection that has one.
  if (value && !options.some((o) => o.value === value)) {
    options.push({ value, label: value });
  }
  return (
    <ConfigSelect
      label="OpenAPI Catalog"
      value={value}
      onChange={(v) =>
        onChange({ ...config, catalog_id: v === "" ? undefined : v })
      }
      options={options}
      help={
        <>
          Catalogs are managed under{" "}
          <a className="underline" href={catalogsHref()}>
            API Catalogs
          </a>
          . One catalog can back many connections. {isLoading && "Loading…"}
        </>
      }
    />
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
      <Alert variant="warning">
        <AlertTriangle />
        <AlertDescription>
          <span>
            This connection still carries a deprecated inline <code>openapi_spec</code> field.
            It is no longer read by the toolkit; the catalog above is what the model sees.
          </span>
          <Button
            type="button"
            variant="link"
            size="xs"
            onClick={() => {
              const next = { ...config };
              delete next.openapi_spec;
              onChange(next);
            }}
          >
            Clear it
          </Button>
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <Alert variant="warning">
      <AlertTriangle />
      <AlertDescription>
        This connection uses an inline OpenAPI spec which is no longer supported.
        Create a catalog under{" "}
        <a className="underline" href={catalogsHref()}>
          API Catalogs
        </a>{" "}
        and select it above. Until you do, <code>api_discover</code> returns no operations.
      </AlertDescription>
    </Alert>
  );
}

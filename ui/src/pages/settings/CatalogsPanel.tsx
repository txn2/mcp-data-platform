import { useEffect, useMemo, useState } from "react";
import { Plus } from "lucide-react";

import {
  type APICatalogSummary,
  useAPICatalogs,
  useEmbeddingProviderStatus,
  useSystemInfo,
} from "@/api/admin/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { CatalogCreateForm } from "./catalogs/CatalogCreateForm";
import { CatalogEditor } from "./catalogs/CatalogEditor";
import { CatalogListItem } from "./catalogs/CatalogListItem";

// Preserve the historical import surface: SpecsManager and SourceBadge
// were exported from this module before the panel was decomposed into
// ./catalogs/. Re-export them here so existing importers (and the panel
// test) keep working without edits.
export { SpecsManager } from "./catalogs/SpecsManager";
export { SourceBadge } from "./catalogs/badges";

// CatalogsPanel is the operator-facing surface for API catalogs:
// globally-owned bundles of OpenAPI 3.x component specs that an
// api-kind connection references via config.catalog_id. Catalogs
// are versioned (each (name, version) is its own row), specs
// inside a catalog are named (constituent, gift, action, ...), and
// mutations fan out to live connections so api_discover reflects
// the new content without a process restart.

export function CatalogsPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: catalogs, isLoading, isFetching } = useAPICatalogs();
  const { data: embedStatus } = useEmbeddingProviderStatus();
  const embedderUnconfigured = embedStatus?.status === "unconfigured";

  const initialSelection = useMemo(() => {
    if (typeof window === "undefined") return null;
    const params = new URLSearchParams(window.location.search);
    return params.get("catalog");
  }, []);

  const [selectedID, setSelectedID] = useState<string | null>(initialSelection);
  const [mode, setMode] = useState<"view" | "create">("view");

  // Wait for any in-flight refetch (after create/edit/clone) to land before
  // deciding the selection is "stale". Otherwise setSelectedID(newID) races
  // the cache invalidation: the effect sees the pre-mutation catalog list,
  // can't find newID, and resets to catalogs[0].
  useEffect(() => {
    if (!catalogs || catalogs.length === 0) return;
    if (isFetching) return;
    if (selectedID && catalogs.some((c) => c.id === selectedID)) return;
    setSelectedID(catalogs[0]?.id ?? null);
  }, [catalogs, selectedID, isFetching]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const params = new URLSearchParams(window.location.search);
    if (selectedID) {
      params.set("catalog", selectedID);
    } else {
      params.delete("catalog");
    }
    const qs = params.toString();
    const url = `${window.location.pathname}${qs ? `?${qs}` : ""}`;
    window.history.replaceState(null, "", url);
  }, [selectedID]);

  const groupedByName = useMemo(() => {
    const groups: Record<string, APICatalogSummary[]> = {};
    for (const c of catalogs ?? []) {
      groups[c.name] = groups[c.name] || [];
      groups[c.name]!.push(c);
    }
    for (const list of Object.values(groups)) {
      list.sort((a, b) => (a.version ?? "").localeCompare(b.version ?? ""));
    }
    return groups;
  }, [catalogs]);

  return (
    <div className="flex h-full min-h-0 flex-col gap-4">
      <PageHeader
        title="API Catalogs"
        // PageHeader wraps its action below a full-width subtitle (flex-wrap
        // wraps before it shrinks), so the prose declares its own measure and
        // the New-catalog button stays on the title's row.
        subtitle={
          <span className="block max-w-2xl">
            Versioned bundles of OpenAPI 3.x specs that api-kind connections share.
            One catalog can back many connections; one Salesforce catalog serves both
            the sandbox and production connections in a deployment.
          </span>
        }
        actions={
          !isReadOnly && (
            <Button
              type="button"
              onClick={() => {
                setSelectedID(null);
                setMode("create");
              }}
            >
              <Plus /> New catalog
            </Button>
          )
        }
      />

      {isReadOnly && (
        <Alert variant="warning">
          <AlertDescription>
            The platform is running in file config mode. Catalog edits are disabled.
          </AlertDescription>
        </Alert>
      )}

      {embedderUnconfigured && (
        // Alert hard-codes role="alert" (assertive); this banner is derived
        // from a polled status query, so it announces politely instead.
        <Alert variant="warning" role="status">
          <AlertDescription>
            <span>
              <strong>Embedding provider not configured.</strong> Semantic ranking is
              disabled; spec saves will not produce per-operation embeddings and
              api_discover falls back to lexical scoring. Set{" "}
              <code className="rounded bg-current/10 px-1 py-0.5 font-mono text-xs">
                memory.embedding.provider
              </code>{" "}
              (e.g., to <code className="font-mono">ollama</code>) and restart to
              enable.
            </span>
          </AlertDescription>
        </Alert>
      )}

      <div className="grid min-h-0 flex-1 grid-cols-[280px_minmax(0,1fr)] gap-4">
        <aside className="overflow-y-auto rounded-md border bg-card">
          {isLoading ? (
            <p className="p-3 text-sm text-muted-foreground">Loading…</p>
          ) : catalogs && catalogs.length === 0 ? (
            <p className="p-3 text-sm text-muted-foreground">
              No catalogs yet. Click <strong>New catalog</strong> to add one.
            </p>
          ) : (
            <ul className="divide-y">
              {Object.keys(groupedByName)
                .sort()
                .map((name) => {
                  const group = groupedByName[name]!;
                  // Single-version catalog: render the item directly. The
                  // slug header is only useful when two or more versions
                  // share a name and the operator needs to disambiguate.
                  if (group.length === 1) {
                    const c = group[0]!;
                    return (
                      <li key={name}>
                        <CatalogListItem
                          catalog={c}
                          selected={selectedID === c.id && mode === "view"}
                          onSelect={() => {
                            setSelectedID(c.id);
                            setMode("view");
                          }}
                        />
                      </li>
                    );
                  }
                  return (
                    <li key={name} className="py-1.5">
                      <div className="px-3 pb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                        {name}
                      </div>
                      <ul>
                        {group.map((c) => (
                          <li key={c.id}>
                            <CatalogListItem
                              catalog={c}
                              selected={selectedID === c.id && mode === "view"}
                              onSelect={() => {
                                setSelectedID(c.id);
                                setMode("view");
                              }}
                              showVersion
                            />
                          </li>
                        ))}
                      </ul>
                    </li>
                  );
                })}
            </ul>
          )}
        </aside>

        <section className="overflow-y-auto rounded-md border bg-card p-4">
          {mode === "create" ? (
            <CatalogCreateForm
              onCancel={() => setMode("view")}
              onCreated={(id) => {
                setSelectedID(id);
                setMode("view");
              }}
              existingIDs={(catalogs ?? []).map((c) => c.id)}
            />
          ) : selectedID ? (
            <CatalogEditor
              catalogID={selectedID}
              isReadOnly={isReadOnly}
              onDeleted={() => {
                setSelectedID(null);
                setMode("view");
              }}
            />
          ) : (
            <EmptyState className="my-8">
              Select a catalog from the left or click{" "}
              <strong className="mx-1">New catalog</strong> to create one.
            </EmptyState>
          )}
        </section>
      </div>
    </div>
  );
}

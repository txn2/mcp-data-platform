import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  BookOpen,
  Check,
  Copy,
  FileText,
  Link as LinkIcon,
  Plus,
  RefreshCw,
  Trash2,
  Upload,
  X,
} from "lucide-react";

import {
  type APICatalogSpec,
  type APICatalogSummary,
  useAPICatalog,
  useAPICatalogSpec,
  useAPICatalogs,
  useCloneAPICatalog,
  useCreateAPICatalog,
  useDeleteAPICatalog,
  useAPICatalogEmbeddingHealth,
  useAPICatalogEmbeddingStatuses,
  useDeleteAPICatalogSpec,
  useEmbeddingProviderStatus,
  useManualRetryEmbedding,
  useRefreshAPICatalogSpec,
  useUpdateAPICatalog,
  useUploadAPICatalogSpec,
  useUpsertAPICatalogSpec,
  useSystemInfo,
  type APICatalogEmbeddingSpecStatus,
} from "@/api/admin/hooks";
import { apiFetch } from "@/api/admin/client";
import { cn } from "@/lib/utils";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { PromptDialog } from "@/components/PromptDialog";

// CatalogsPanel is the operator-facing surface for API catalogs:
// globally-owned bundles of OpenAPI 3.x component specs that an
// api-kind connection references via config.catalog_id. Catalogs
// are versioned (each (name, version) is its own row), specs
// inside a catalog are named (constituent, gift, action, ...), and
// mutations fan out to live connections so api_list_endpoints
// and api_get_endpoint_schema reflect the new content without a
// process restart.

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
      <header className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold">API Catalogs</h1>
          <p className="text-sm text-muted-foreground">
            Versioned bundles of OpenAPI 3.x specs that api-kind connections share.
            One catalog can back many connections; one Salesforce catalog serves
            both the sandbox and production connections in a deployment.
          </p>
        </div>
        {!isReadOnly && (
          <button
            type="button"
            onClick={() => {
              setSelectedID(null);
              setMode("create");
            }}
            className="inline-flex items-center gap-1 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90"
          >
            <Plus className="h-4 w-4" /> New catalog
          </button>
        )}
      </header>

      {isReadOnly && (
        <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200">
          The platform is running in file config mode. Catalog edits are disabled.
        </div>
      )}

      {embedderUnconfigured && (
        <div
          role="status"
          className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
        >
          <strong>Embedding provider not configured.</strong> Semantic ranking is
          disabled; spec saves will not produce per-operation embeddings and
          api_list_endpoints falls back to lexical scoring. Set{" "}
          <code className="rounded bg-amber-100 px-1 py-0.5 font-mono text-xs dark:bg-amber-900/40">
            memory.embedding.provider
          </code>{" "}
          (e.g., to <code className="font-mono">ollama</code>) and restart to
          enable.
        </div>
      )}

      <div className="grid min-h-0 flex-1 grid-cols-[280px_minmax(0,1fr)] gap-4">
        <aside className="overflow-y-auto rounded-md border bg-card">
          {isLoading ? (
            <div className="p-3 text-sm text-muted-foreground">Loading…</div>
          ) : catalogs && catalogs.length === 0 ? (
            <div className="p-3 text-sm text-muted-foreground">
              No catalogs yet. Click <strong>New catalog</strong> to add one.
            </div>
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
            <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
              Select a catalog from the left or click <strong className="mx-1">New catalog</strong> to create one.
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

// CatalogListItem is one row in the left-nav catalog list. When the
// row belongs to a multi-version group, showVersion=true causes the
// version label to render as an inline chip so the operator can pick
// the right version under the shared slug header. For single-version
// catalogs the version is omitted from the row (it stays visible in
// the editor header) so the list stays uncluttered.
function CatalogListItem({
  catalog,
  selected,
  onSelect,
  showVersion,
}: {
  catalog: APICatalogSummary;
  selected: boolean;
  onSelect: () => void;
  showVersion?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={cn(
        "block w-full rounded px-3 py-2 text-left text-sm hover:bg-muted",
        selected && "bg-muted",
      )}
    >
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate">{catalog.display_name}</span>
        {showVersion && catalog.version && (
          <span className="shrink-0 rounded bg-muted-foreground/10 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
            v{catalog.version}
          </span>
        )}
      </div>
      <div className="text-xs text-muted-foreground">
        {catalog.spec_count} spec{catalog.spec_count === 1 ? "" : "s"}
        {catalog.ref_count > 0 ? (
          <span> · {catalog.ref_count} connection{catalog.ref_count === 1 ? "" : "s"}</span>
        ) : null}
      </div>
    </button>
  );
}

// ---------------------------------------------------------------------------
// Create form
// ---------------------------------------------------------------------------

// Slugify a free-text human label into the lowercase-hyphenated form
// accepted by the catalog name field. Used to auto-derive the
// machine-readable slug from whatever the operator types as the
// display name.
function slugifyName(raw: string): string {
  return raw
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function suggestSlug(name: string, version: string): string {
  const baseName = slugifyName(name);
  if (!baseName) return "";
  const baseVer = slugifyName(version);
  return baseVer ? `${baseName}-${baseVer}` : baseName;
}

// currentYearMonth returns the current calendar month formatted as
// YYYY-MM, used as a sensible default for new catalog version
// labels. Operators can still edit it.
function currentYearMonth(): string {
  const d = new Date();
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  return `${yyyy}-${mm}`;
}

// normalizeSpecName mirrors the server's ValidateSpecName contract
// (pkg/toolkits/apigateway/catalog/catalog.go): lowercase letters,
// digits, hyphens, and underscores; must start and end with a
// letter or digit. Typed input is lowercased, spaces collapsed to
// hyphens, out-of-range characters stripped, and leading/trailing
// hyphens or underscores trimmed so the operator never has to
// guess at the server's slug rule.
function normalizeSpecName(raw: string): string {
  return raw
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9_-]/g, "")
    .replace(/^[-_]+/, "")
    .replace(/[-_]+$/, "");
}

function CatalogCreateForm({
  onCancel,
  onCreated,
  existingIDs,
}: {
  onCancel: () => void;
  onCreated: (id: string) => void;
  existingIDs: string[];
}) {
  const [displayName, setDisplayName] = useState("");
  const [version, setVersion] = useState(currentYearMonth());
  const [name, setName] = useState("");
  const [id, setID] = useState("");
  const [description, setDescription] = useState("");
  const [touchedName, setTouchedName] = useState(false);
  const [touchedID, setTouchedID] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const create = useCreateAPICatalog();

  // Auto-derive the internal slug from the display name until the
  // operator types one explicitly. Mirrors the title->slug pattern
  // used in WordPress, GitHub repo creation, etc.
  useEffect(() => {
    if (touchedName) return;
    setName(slugifyName(displayName));
  }, [displayName, touchedName]);

  // Auto-derive the catalog ID from name + version until the
  // operator types one explicitly.
  useEffect(() => {
    if (touchedID) return;
    setID(suggestSlug(name, version));
  }, [name, version, touchedID]);

  const idConflict = existingIDs.includes(id);

  const submit = useCallback(async () => {
    setError(null);
    if (!name || !displayName || !id) {
      setError("display name, internal slug, and catalog ID are required");
      return;
    }
    try {
      const created = await create.mutateAsync({
        id,
        name,
        version: version || undefined,
        display_name: displayName,
        description: description || undefined,
      });
      onCreated(created.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : "create failed");
    }
  }, [create, id, name, version, displayName, description, onCreated]);

  return (
    <div className="max-w-2xl space-y-4">
      <h2 className="flex items-center gap-2 text-lg font-medium">
        <BookOpen className="h-5 w-5" /> New API Catalog
      </h2>

      <LabeledInput
        label="Catalog name"
        help="Human-readable name shown in the catalog list and the connection editor's dropdown. Example: 'Salesforce REST'."
        value={displayName}
        onChange={setDisplayName}
        placeholder="Salesforce REST"
      />
      <LabeledInput
        label="Version"
        help="Free-text label that distinguishes versions of the same catalog over time. Defaults to the current month (YYYY-MM)."
        value={version}
        onChange={setVersion}
        placeholder={currentYearMonth()}
        mono
      />
      <LabeledInput
        label="Internal slug"
        help="Machine-readable family slug shared across versions of the same API (e.g. all Salesforce REST catalogs use 'salesforce-rest'). Auto-derived from the catalog name; edit if you need a different grouping."
        value={name}
        onChange={(v) => {
          setTouchedName(true);
          setName(v);
        }}
        placeholder="salesforce-rest"
        mono
      />
      <LabeledInput
        label="Catalog ID"
        help="Immutable identifier used in URLs and the connection.catalog_id field. Auto-derived from slug + version; cannot change after creation."
        value={id}
        onChange={(v) => {
          setTouchedID(true);
          setID(v);
        }}
        placeholder="salesforce-rest-2024-10"
        mono
        invalid={idConflict}
        error={idConflict ? "id already exists" : undefined}
      />
      <LabeledTextarea
        label="Description"
        help="Optional operator-facing notes."
        value={description}
        onChange={setDescription}
      />

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={submit}
          disabled={create.isPending || idConflict || !id || !name || !displayName}
          className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
        >
          {create.isPending ? "Creating…" : "Create"}
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Catalog editor (header + SpecsManager)
// ---------------------------------------------------------------------------

function CatalogEditor({
  catalogID,
  isReadOnly,
  onDeleted,
}: {
  catalogID: string;
  isReadOnly: boolean;
  onDeleted: () => void;
}) {
  const { data: catalog, isLoading } = useAPICatalog(catalogID);
  const update = useUpdateAPICatalog();
  const del = useDeleteAPICatalog();
  const clone = useCloneAPICatalog();

  const [editing, setEditing] = useState(false);
  const [draftName, setDraftName] = useState("");
  const [draftVersion, setDraftVersion] = useState("");
  const [draftDisplayName, setDraftDisplayName] = useState("");
  const [draftDescription, setDraftDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [cloneOpen, setCloneOpen] = useState(false);
  const [cloneError, setCloneError] = useState<string | null>(null);

  useEffect(() => {
    if (catalog) {
      setDraftName(catalog.name);
      setDraftVersion(catalog.version ?? "");
      setDraftDisplayName(catalog.display_name);
      setDraftDescription(catalog.description ?? "");
    }
  }, [catalog]);

  const handleSave = async () => {
    setError(null);
    try {
      await update.mutateAsync({
        id: catalogID,
        name: draftName,
        version: draftVersion,
        display_name: draftDisplayName,
        description: draftDescription,
      });
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    }
  };

  const handleDeleteConfirmed = async () => {
    setDeleteError(null);
    try {
      await del.mutateAsync(catalogID);
      setConfirmDeleteOpen(false);
      onDeleted();
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : "delete failed");
    }
  };

  const handleCloneConfirmed = async (values: Record<string, string>) => {
    const newID = values.id?.trim();
    if (!newID) return;
    setCloneError(null);
    try {
      await clone.mutateAsync({
        sourceID: catalogID,
        id: newID,
        name: catalog?.name,
        version: values.version?.trim() || undefined,
      });
      setCloneOpen(false);
    } catch (e) {
      setCloneError(e instanceof Error ? e.message : "clone failed");
    }
  };

  if (isLoading || !catalog) {
    return <div className="text-sm text-muted-foreground">Loading…</div>;
  }

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        {!isReadOnly && (
          <div className="flex flex-wrap justify-end gap-2">
            {editing ? (
              <>
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={update.isPending}
                  className="inline-flex items-center gap-1 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
                >
                  <Check className="h-4 w-4" /> Save
                </button>
                <button
                  type="button"
                  onClick={() => setEditing(false)}
                  className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
                >
                  Cancel
                </button>
              </>
            ) : (
              <>
                <button
                  type="button"
                  onClick={() => setEditing(true)}
                  className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
                >
                  Edit
                </button>
                <button
                  type="button"
                  onClick={() => setCloneOpen(true)}
                  className="inline-flex items-center gap-1 rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
                >
                  <Copy className="h-4 w-4" /> Clone
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmDeleteOpen(true)}
                  disabled={catalog.ref_count > 0}
                  title={catalog.ref_count > 0 ? "Cannot delete; still referenced by a connection" : ""}
                  className="inline-flex items-center gap-1 rounded-md border bg-background px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10 disabled:opacity-50"
                >
                  <Trash2 className="h-4 w-4" /> Delete
                </button>
              </>
            )}
          </div>
        )}

        {editing ? (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 md:max-w-2xl">
            <div className="md:col-span-2">
              <LabeledInput
                label="Catalog name"
                help="Human-readable name shown to operators."
                value={draftDisplayName}
                onChange={setDraftDisplayName}
              />
            </div>
            <LabeledInput
              label="Internal slug"
              help="Machine-readable family slug shared across versions."
              value={draftName}
              onChange={setDraftName}
              mono
            />
            <LabeledInput
              label="Version"
              help="Free-text version label."
              value={draftVersion}
              onChange={setDraftVersion}
              mono
            />
            <div className="md:col-span-2">
              <LabeledTextarea
                label="Description"
                value={draftDescription}
                onChange={setDraftDescription}
              />
            </div>
          </div>
        ) : (
          <div>
            <h2 className="text-lg font-semibold break-words">{catalog.display_name}</h2>
            <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <code className="break-all">{catalog.id}</code>
              {catalog.version && (
                <span className="rounded bg-muted px-1.5 py-0.5">v{catalog.version}</span>
              )}
              {catalog.ref_count > 0 && (
                <span>· referenced by {catalog.ref_count} connection{catalog.ref_count === 1 ? "" : "s"}</span>
              )}
            </div>
            {catalog.description && (
              <p className="mt-2 text-sm text-muted-foreground">{catalog.description}</p>
            )}
          </div>
        )}
      </div>

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <SpecsManager catalogID={catalogID} isReadOnly={isReadOnly} />

      <ConfirmDialog
        open={confirmDeleteOpen}
        onOpenChange={(open) => {
          setConfirmDeleteOpen(open);
          if (!open) setDeleteError(null);
        }}
        destructive
        title="Delete catalog?"
        description={
          <>
            The catalog <code className="font-mono">{catalog.id}</code> and all
            of its component specs will be removed. This cannot be undone.
          </>
        }
        confirmLabel="Delete"
        loading={del.isPending}
        error={deleteError}
        onConfirm={handleDeleteConfirmed}
      />

      <PromptDialog
        open={cloneOpen}
        onOpenChange={(open) => {
          setCloneOpen(open);
          if (!open) setCloneError(null);
        }}
        title="Clone catalog"
        description={
          <>
            Clones the catalog header and every component spec into a new
            row. Pick a new ID (immutable) and an optional new version.
          </>
        }
        fields={[
          {
            name: "id",
            label: "New catalog ID",
            placeholder: "salesforce-rest-2025-01",
            required: true,
            monospace: true,
            help: "Lowercase, hyphens, no spaces. Immutable after creation.",
          },
          {
            name: "version",
            label: "Version (optional)",
            placeholder: "2025-01",
            monospace: true,
            help: "Free-text label. Leave blank to clone without a version label.",
          },
        ]}
        confirmLabel="Clone"
        loading={clone.isPending}
        error={cloneError}
        onConfirm={handleCloneConfirmed}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// SpecsManager + SpecModal
// ---------------------------------------------------------------------------

function SpecsManager({ catalogID, isReadOnly }: { catalogID: string; isReadOnly: boolean }) {
  const [specs, setSpecs] = useState<APICatalogSpec[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshCounter, setRefreshCounter] = useState(0);
  const [editing, setEditing] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const refresh = useRefreshAPICatalogSpec();
  const manualRetry = useManualRetryEmbedding();
  const del = useDeleteAPICatalogSpec();
  // The job-queue-backed embedding state polls every 5s while
  // the panel is mounted. The badge updates as the worker
  // progresses; the catalog header summary reflects pending /
  // failed counts. Operators do not need to take any action.
  const { data: health } = useAPICatalogEmbeddingHealth(catalogID);
  const { data: statusList } = useAPICatalogEmbeddingStatuses(catalogID);
  const statusByName = useMemo(() => {
    const map: Record<string, APICatalogEmbeddingSpecStatus> = {};
    for (const s of statusList?.specs ?? []) {
      map[s.spec_name] = s;
    }
    return map;
  }, [statusList]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    void apiFetch<{ specs?: APICatalogSpec[] }>(`/api-catalogs/${catalogID}/specs`)
      .then((res) => {
        if (!cancelled) setSpecs(res.specs ?? []);
      })
      .catch(() => {
        if (!cancelled) setSpecs([]);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [catalogID, refreshCounter]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Component specs</h3>
        {!isReadOnly && (
          <button
            type="button"
            onClick={() => setAdding(true)}
            className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-xs hover:bg-muted"
          >
            <Plus className="h-3.5 w-3.5" /> Add spec
          </button>
        )}
      </div>

      {health && <CatalogEmbeddingHealthBanner health={health} />}

      {loading ? (
        <div className="text-sm text-muted-foreground">Loading…</div>
      ) : specs.length === 0 ? (
        <div className="rounded-md border bg-muted/30 p-4 text-sm text-muted-foreground">
          No specs yet. Add one to expose endpoints on the connections that reference this catalog.
        </div>
      ) : (
        <ul className="divide-y rounded-md border">
          {specs.map((s) => {
            const status = statusByName[s.spec_name];
            const failed = status?.job_status === "failed";
            return (
              <li key={s.spec_name} className="flex items-center gap-3 px-3 py-2 text-sm">
                <span className="flex-1 truncate font-mono">{s.spec_name}</span>
                <SourceBadge kind={s.source_kind} url={s.source_url} />
                <EmbeddingStatusBadge status={status} />
                {s.last_fetched_at && (
                  <span className="text-xs text-muted-foreground">
                    fetched {new Date(s.last_fetched_at).toLocaleString()}
                  </span>
                )}
                {!isReadOnly && (
                  <div className="flex gap-1">
                    {failed && (
                      <button
                        type="button"
                        onClick={() =>
                          manualRetry.mutate(
                            { catalogID, specName: s.spec_name },
                            { onSuccess: () => setRefreshCounter((n) => n + 1) },
                          )
                        }
                        title={`Retry embedding (last error: ${status?.job_last_error ?? "unknown"})`}
                        className="rounded px-2 py-1 text-xs text-destructive hover:bg-destructive/10"
                      >
                        Retry
                      </button>
                    )}
                    {s.source_kind === "url" && (
                      <button
                        type="button"
                        onClick={() =>
                          refresh.mutate(
                            { catalogID, specName: s.spec_name },
                            { onSuccess: () => setRefreshCounter((n) => n + 1) },
                          )
                        }
                        title="Refresh from URL"
                        className="rounded p-1 hover:bg-muted"
                      >
                        <RefreshCw className="h-4 w-4" />
                      </button>
                    )}
                    <button
                      type="button"
                      onClick={() => setEditing(s.spec_name)}
                      className="rounded p-1 hover:bg-muted"
                    >
                      Edit
                    </button>
                    <button
                      type="button"
                      onClick={() => setPendingDelete(s.spec_name)}
                      className="rounded p-1 text-destructive hover:bg-destructive/10"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {(adding || editing) && (
        <SpecModal
          catalogID={catalogID}
          existingSpecName={editing ?? undefined}
          onClose={() => {
            setAdding(false);
            setEditing(null);
          }}
          onSaved={() => {
            setAdding(false);
            setEditing(null);
            setRefreshCounter((n) => n + 1);
          }}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open) {
            setPendingDelete(null);
            setDeleteError(null);
          }
        }}
        destructive
        title="Delete component spec?"
        description={
          pendingDelete ? (
            <>
              The spec <code className="font-mono">{pendingDelete}</code> will
              be removed from this catalog. Connections referencing the
              catalog reload immediately and stop seeing operations from
              this spec.
            </>
          ) : null
        }
        confirmLabel="Delete"
        loading={del.isPending}
        error={deleteError}
        onConfirm={async () => {
          if (!pendingDelete) return;
          setDeleteError(null);
          try {
            await del.mutateAsync({ catalogID, specName: pendingDelete });
            setRefreshCounter((n) => n + 1);
            setPendingDelete(null);
          } catch (e) {
            setDeleteError(e instanceof Error ? e.message : "delete failed");
          }
        }}
      />
    </div>
  );
}

// EmbeddingStatusBadge surfaces the per-spec embedding state
// computed by the job queue. The badge color and label
// communicate one of five states the operator can react to:
//
//   green:  "N/M indexed"     — spec is fully indexed; semantic
//                                ranking is active.
//   blue:   "indexing N/M"    — a worker is currently embedding
//                                this spec; counts move as the
//                                worker progresses.
//   amber:  "queued"          — the job is in the queue waiting
//                                for a worker to pick it up.
//   red:    "failed (last_error)" — the job exhausted retries.
//                                The operator can click Retry
//                                next to the badge to force a
//                                fresh attempt.
//   gray:   "not indexed"     — the spec has no operations or
//                                no job has run for it (legacy
//                                state cleared by the next
//                                reconciler tick).
function EmbeddingStatusBadge({ status }: { status?: APICatalogEmbeddingSpecStatus }) {
  if (!status) {
    return (
      <span
        title="Embedding status not yet loaded"
        className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground"
      >
        loading…
      </span>
    );
  }
  const fully =
    status.operation_count > 0 && status.embedding_count === status.operation_count;
  const jobStatus = status.job_status ?? "";
  if (fully && (jobStatus === "succeeded" || jobStatus === "")) {
    return (
      <span
        title={`${status.embedding_count}/${status.operation_count} operations indexed; semantic ranking active`}
        className="inline-flex items-center gap-1 rounded bg-emerald-100 px-1.5 py-0.5 text-xs text-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-200"
      >
        {status.embedding_count}/{status.operation_count} indexed
      </span>
    );
  }
  if (jobStatus === "running") {
    // While the spec's UPSERT transaction is still pending, embedding_count
    // sits at 0 (or the previous run's value). The worker publishes
    // embedded_so_far at every chunk boundary so the badge ticks up
    // before the final commit. See #430.
    const progress = status.embedded_so_far ?? status.embedding_count;
    return (
      <span
        title={`Worker is embedding this spec (attempt ${status.job_attempts ?? 1})`}
        className="inline-flex items-center gap-1 rounded bg-sky-100 px-1.5 py-0.5 text-xs text-sky-900 dark:bg-sky-950/30 dark:text-sky-200"
      >
        indexing {progress}/{status.operation_count}
      </span>
    );
  }
  if (jobStatus === "pending") {
    return (
      <span
        title="Queued for embedding"
        className="inline-flex items-center gap-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200"
      >
        queued
      </span>
    );
  }
  if (jobStatus === "failed") {
    return (
      <span
        title={status.job_last_error || "embedding failed"}
        className="inline-flex items-center gap-1 rounded bg-destructive/15 px-1.5 py-0.5 text-xs text-destructive"
      >
        failed
      </span>
    );
  }
  if (status.operation_count === 0) {
    return (
      <span
        title="Spec has zero operations; nothing to embed"
        className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground"
      >
        empty
      </span>
    );
  }
  return (
    <span
      title="No embedding job has run for this spec yet; reconciler will pick it up"
      className="inline-flex items-center gap-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200"
    >
      not indexed
    </span>
  );
}

// CatalogEmbeddingHealthBanner is the one-line summary at the
// top of the spec list. Operators check it before considering
// the catalog production-ready ("All specs indexed" is the
// green-light signal; a non-zero pending/failed count means
// the worker is still catching up or attention is needed).
function CatalogEmbeddingHealthBanner({
  health,
}: {
  health: { specs_total: number; specs_indexed: number; specs_pending: number; specs_running: number; specs_failed: number };
}) {
  if (health.specs_total === 0) {
    return null;
  }
  const allIndexed =
    health.specs_indexed === health.specs_total &&
    health.specs_pending === 0 &&
    health.specs_running === 0 &&
    health.specs_failed === 0;
  if (allIndexed) {
    return (
      <div className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-100">
        All {health.specs_total} specs indexed. Semantic ranking is active across this catalog.
      </div>
    );
  }
  const parts: string[] = [];
  if (health.specs_running > 0) parts.push(`${health.specs_running} running`);
  if (health.specs_pending > 0) parts.push(`${health.specs_pending} queued`);
  if (health.specs_failed > 0) parts.push(`${health.specs_failed} failed`);
  return (
    <div
      className={cn(
        "rounded-md border px-3 py-2 text-xs",
        health.specs_failed > 0
          ? "border-destructive/40 bg-destructive/10 text-destructive"
          : "border-amber-500/30 bg-amber-500/10 text-amber-100",
      )}
    >
      {health.specs_indexed}/{health.specs_total} specs indexed
      {parts.length > 0 ? ` (${parts.join(", ")})` : ""}
    </div>
  );
}

function SourceBadge({ kind, url }: { kind: APICatalogSpec["source_kind"]; url?: string }) {
  const config = {
    inline: { icon: FileText, label: "inline", tone: "bg-muted text-muted-foreground" },
    upload: { icon: Upload, label: "upload", tone: "bg-blue-100 text-blue-900 dark:bg-blue-950/30 dark:text-blue-200" },
    url: { icon: LinkIcon, label: "URL", tone: "bg-green-100 text-green-900 dark:bg-green-950/30 dark:text-green-200" },
  }[kind];
  const Icon = config.icon;
  return (
    <span
      title={url || undefined}
      className={cn("inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs", config.tone)}
    >
      <Icon className="h-3 w-3" /> {config.label}
    </span>
  );
}

// ---------------------------------------------------------------------------
// SpecModal — three-tab spec add/edit
// ---------------------------------------------------------------------------

type SourceTab = "paste" | "upload" | "url";

function SpecModal({
  catalogID,
  existingSpecName,
  onClose,
  onSaved,
}: {
  catalogID: string;
  existingSpecName?: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEditing = !!existingSpecName;
  const { data: existing } = useAPICatalogSpec(catalogID, existingSpecName ?? "", isEditing);
  const upsert = useUpsertAPICatalogSpec();
  const upload = useUploadAPICatalogSpec();

  const [specName, setSpecName] = useState(existingSpecName ?? "");
  const [tab, setTab] = useState<SourceTab>("paste");
  const [content, setContent] = useState("");
  const [sourceURL, setSourceURL] = useState("");
  const [basePath, setBasePath] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!existing) return;
    if (existing.source_kind === "inline" || existing.source_kind === "upload") {
      setContent(existing.content ?? "");
      setTab(existing.source_kind === "upload" ? "upload" : "paste");
    }
    if (existing.source_kind === "url") {
      setSourceURL(existing.source_url ?? "");
      setTab("url");
    }
    setBasePath(existing.base_path ?? "");
  }, [existing]);

  const submit = useCallback(async () => {
    setError(null);
    if (!specName) {
      setError("spec name is required");
      return;
    }
    try {
      if (tab === "paste") {
        await upsert.mutateAsync({
          catalogID,
          specName,
          source_kind: "inline",
          content,
          base_path: basePath.trim(),
        });
      } else if (tab === "url") {
        await upsert.mutateAsync({
          catalogID,
          specName,
          source_kind: "url",
          source_url: sourceURL,
          base_path: basePath.trim(),
        });
      } else if (tab === "upload") {
        if (!file) {
          setError("choose a file");
          return;
        }
        if (file.size > 10 * 1024 * 1024) {
          setError("file exceeds 10 MB limit");
          return;
        }
        await upload.mutateAsync({
          catalogID,
          specName,
          file,
          base_path: basePath.trim(),
        });
      }
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    }
  }, [catalogID, specName, tab, content, sourceURL, basePath, file, upsert, upload, onSaved]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div className="w-full max-w-3xl rounded-md border bg-card shadow-lg">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="text-base font-medium">
            {isEditing ? `Edit spec — ${existingSpecName}` : "Add component spec"}
          </h3>
          <button
            type="button"
            onClick={onClose}
            className="rounded p-1 hover:bg-muted"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 px-4 py-4">
          <LabeledInput
            label="Spec name"
            help={
              "A short label for this component within the catalog. Use 'default' if the catalog has one spec. Use multiple names (e.g. drive, gmail) only when the catalog bundles separate APIs; the model sees this label in the spec field of api_list_endpoints so it can pick the right operation. Lowercase letters, digits, hyphens, or underscores; typed input is auto-lowercased."
            }
            value={specName}
            onChange={(v) => setSpecName(normalizeSpecName(v))}
            mono
            disabled={isEditing}
            placeholder="default"
          />

          <div className="flex gap-2 border-b">
            {(["paste", "upload", "url"] as SourceTab[]).map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => setTab(t)}
                className={cn(
                  "border-b-2 px-3 py-1.5 text-sm",
                  tab === t
                    ? "border-primary text-primary"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                {t === "paste" ? "Paste" : t === "upload" ? "Upload" : "URL"}
              </button>
            ))}
          </div>

          {tab === "paste" && (
            <LabeledTextarea
              label="OpenAPI YAML or JSON"
              value={content}
              onChange={setContent}
              placeholder="openapi: 3.0.0&#10;info:&#10;  title: Vendor&#10;..."
              rows={14}
              mono
            />
          )}

          {tab === "upload" && (
            <div>
              <label className="mb-1 block text-xs font-medium">Spec file</label>
              <input
                type="file"
                accept=".yaml,.yml,.json,application/yaml,application/json,text/yaml"
                onChange={(e) => {
                  const f = e.target.files?.[0] ?? null;
                  setFile(f);
                  if (f && !specName && !isEditing) {
                    setSpecName(normalizeSpecName(f.name.replace(/\.(ya?ml|json)$/i, "")));
                  }
                }}
                className="block text-sm"
              />
              <p className="mt-1 text-xs text-muted-foreground">
                Max 10 MB. YAML or JSON. The server validates the content as OpenAPI 3.x before saving.
              </p>
            </div>
          )}

          {tab === "url" && (
            <LabeledInput
              label="Spec URL"
              help="HTTPS URL to a publicly reachable OpenAPI document. The server fetches once at save and stores the content; click Refresh on the spec row to re-fetch."
              value={sourceURL}
              onChange={setSourceURL}
              placeholder="https://petstore3.swagger.io/api/v3/openapi.json"
              mono
            />
          )}

          <LabeledInput
            label="Base path (optional)"
            help="URL path segment prepended to every operation in this spec at invoke time. Set this when the spec ships without a servers[] entry, or when you need to override the spec author's value (sandbox, proxy, version pin). When empty, the toolkit derives the prefix from the spec's first servers[].url. Must start with '/'. Example: /v1 or /api/v2."
            value={basePath}
            onChange={setBasePath}
            placeholder="/v1"
            mono
          />

          {error && (
            <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}
        </div>

        <div className="flex justify-end gap-2 border-t px-4 py-3">
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={upsert.isPending || upload.isPending}
            className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
          >
            {upsert.isPending || upload.isPending ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Small form helpers (kept local so the panel stays self-contained)
// ---------------------------------------------------------------------------

function LabeledInput({
  label,
  help,
  value,
  onChange,
  placeholder,
  mono,
  disabled,
  invalid,
  error,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  error?: string;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        className={cn(
          "w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 disabled:opacity-60",
          mono && "font-mono",
          invalid && "border-destructive",
        )}
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
      {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
    </div>
  );
}

function LabeledTextarea({
  label,
  help,
  value,
  onChange,
  placeholder,
  rows,
  mono,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  rows?: number;
  mono?: boolean;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        rows={rows ?? 3}
        className={cn(
          "w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2",
          mono && "font-mono",
        )}
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

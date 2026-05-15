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
  useDeleteAPICatalogSpec,
  useRefreshAPICatalogSpec,
  useUpdateAPICatalog,
  useUploadAPICatalogSpec,
  useUpsertAPICatalogSpec,
  useSystemInfo,
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
  const { data: catalogs, isLoading } = useAPICatalogs();

  const initialSelection = useMemo(() => {
    if (typeof window === "undefined") return null;
    const params = new URLSearchParams(window.location.search);
    return params.get("catalog");
  }, []);

  const [selectedID, setSelectedID] = useState<string | null>(initialSelection);
  const [mode, setMode] = useState<"view" | "create">("view");

  useEffect(() => {
    if (!catalogs || catalogs.length === 0) return;
    if (selectedID && catalogs.some((c) => c.id === selectedID)) return;
    setSelectedID(catalogs[0]?.id ?? null);
  }, [catalogs, selectedID]);

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
                .map((name) => (
                  <li key={name} className="px-2 py-1.5">
                    <div className="px-1 pb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                      {name}
                    </div>
                    <ul>
                      {groupedByName[name]!.map((c) => (
                        <li key={c.id}>
                          <button
                            type="button"
                            onClick={() => {
                              setSelectedID(c.id);
                              setMode("view");
                            }}
                            className={cn(
                              "block w-full rounded px-2 py-1.5 text-left text-sm hover:bg-muted",
                              selectedID === c.id && mode === "view" && "bg-muted",
                            )}
                          >
                            <div className="truncate">{c.display_name}</div>
                            <div className="text-xs text-muted-foreground">
                              {c.spec_count} spec{c.spec_count === 1 ? "" : "s"}
                              {c.ref_count > 0 ? (
                                <span> · {c.ref_count} connection{c.ref_count === 1 ? "" : "s"}</span>
                              ) : null}
                            </div>
                          </button>
                        </li>
                      ))}
                    </ul>
                  </li>
                ))}
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
  // We can read the per-catalog spec list off the catalog's API by listing
  // catalog detail + querying each spec. To keep this panel snappy we
  // fetch the catalog list response (which carries spec_count) and rely
  // on a separate endpoint for the spec list. The admin handler exposes
  // /api-catalogs/{id} with spec_count only — to display rows we fetch
  // each named spec lazily via the per-spec endpoint when expanded.
  const [specs, setSpecs] = useState<APICatalogSpec[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshCounter, setRefreshCounter] = useState(0);
  const [editing, setEditing] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const refresh = useRefreshAPICatalogSpec();
  const del = useDeleteAPICatalogSpec();

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

      {loading ? (
        <div className="text-sm text-muted-foreground">Loading…</div>
      ) : specs.length === 0 ? (
        <div className="rounded-md border bg-muted/30 p-4 text-sm text-muted-foreground">
          No specs yet. Add one to expose endpoints on the connections that reference this catalog.
        </div>
      ) : (
        <ul className="divide-y rounded-md border">
          {specs.map((s) => (
            <li key={s.spec_name} className="flex items-center gap-3 px-3 py-2 text-sm">
              <span className="flex-1 truncate font-mono">{s.spec_name}</span>
              <SourceBadge kind={s.source_kind} url={s.source_url} />
              {s.last_fetched_at && (
                <span className="text-xs text-muted-foreground">
                  fetched {new Date(s.last_fetched_at).toLocaleString()}
                </span>
              )}
              {!isReadOnly && (
                <div className="flex gap-1">
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
          ))}
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
        });
      } else if (tab === "url") {
        await upsert.mutateAsync({
          catalogID,
          specName,
          source_kind: "url",
          source_url: sourceURL,
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
        await upload.mutateAsync({ catalogID, specName, file });
      }
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    }
  }, [catalogID, specName, tab, content, sourceURL, file, upsert, upload, onSaved]);

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

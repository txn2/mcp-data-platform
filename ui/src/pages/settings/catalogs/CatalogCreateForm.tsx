import { useCallback, useEffect, useState } from "react";
import { AlertCircle, BookOpen } from "lucide-react";

import { useCreateAPICatalog } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { LabeledInput, LabeledTextarea } from "./forms";

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

export function CatalogCreateForm({
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
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          Cancel
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={submit}
          disabled={create.isPending || idConflict || !id || !name || !displayName}
        >
          {create.isPending ? "Creating…" : "Create"}
        </Button>
      </div>
    </div>
  );
}

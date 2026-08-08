import { useCallback, useEffect, useId, useState } from "react";
import { AlertCircle, X } from "lucide-react";

import {
  useAPICatalogSpec,
  useUploadAPICatalogSpec,
  useUpsertAPICatalogSpec,
} from "@/api/admin/hooks";
import { ModalShell } from "@/components/ModalShell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { LabeledInput, LabeledTextarea } from "./forms";

// ---------------------------------------------------------------------------
// SpecModal — three-tab spec add/edit
// ---------------------------------------------------------------------------

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

export type SourceTab = "paste" | "upload" | "url";

export function SpecModal({
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
  const { data: existing } = useAPICatalogSpec(
    catalogID,
    existingSpecName ?? "",
    isEditing,
  );
  const upsert = useUpsertAPICatalogSpec();
  const upload = useUploadAPICatalogSpec();

  const [specName, setSpecName] = useState(existingSpecName ?? "");
  const [tab, setTab] = useState<SourceTab>("paste");
  const [content, setContent] = useState("");
  const [sourceURL, setSourceURL] = useState("");
  const [basePath, setBasePath] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fileInputID = useId();

  useEffect(() => {
    if (!existing) return;
    if (
      existing.source_kind === "inline" ||
      existing.source_kind === "upload"
    ) {
      setContent(existing.content ?? "");
      setTab(existing.source_kind === "upload" ? "upload" : "paste");
    }
    if (existing.source_kind === "url") {
      setSourceURL(existing.source_url ?? "");
      setTab("url");
    }
    setBasePath(existing.base_path ?? "");
    setTitle(existing.title ?? "");
    setDescription(existing.description ?? "");
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
          title: title.trim(),
          description: description.trim(),
        });
      } else if (tab === "url") {
        await upsert.mutateAsync({
          catalogID,
          specName,
          source_kind: "url",
          source_url: sourceURL,
          base_path: basePath.trim(),
          title: title.trim(),
          description: description.trim(),
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
          title: title.trim(),
          description: description.trim(),
        });
      }
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    }
  }, [
    catalogID,
    specName,
    tab,
    content,
    sourceURL,
    basePath,
    title,
    description,
    file,
    upsert,
    upload,
    onSaved,
  ]);

  return (
    <ModalShell
      onClose={onClose}
      width="max-w-3xl"
      label={isEditing ? `Edit spec ${existingSpecName}` : "Add component spec"}
      busy={upsert.isPending || upload.isPending}
      header={
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="text-base font-medium">
            {isEditing
              ? `Edit spec — ${existingSpecName}`
              : "Add component spec"}
          </h3>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={onClose}
            aria-label="Close"
          >
            <X />
          </Button>
        </div>
      }
      footer={
        <div className="flex justify-end gap-2 border-t px-4 py-3">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={submit}
            disabled={upsert.isPending || upload.isPending}
          >
            {upsert.isPending || upload.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      }
    >
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

        {/* The tab is the source of the spec's content, and it decides which
            mutation Save issues — so it is a real tab set, not three sections. */}
        <Tabs value={tab} onValueChange={(v) => setTab(v as SourceTab)}>
          <TabsList variant="line">
            <TabsTrigger value="paste">Paste</TabsTrigger>
            <TabsTrigger value="upload">Upload</TabsTrigger>
            <TabsTrigger value="url">URL</TabsTrigger>
          </TabsList>

          <TabsContent value="paste" className="pt-2">
            <LabeledTextarea
              label="OpenAPI YAML or JSON"
              value={content}
              onChange={setContent}
              placeholder="openapi: 3.0.0&#10;info:&#10;  title: Vendor&#10;..."
              rows={14}
              mono
            />
          </TabsContent>

          <TabsContent value="upload" className="space-y-1.5 pt-2">
            <Label htmlFor={fileInputID} className="text-xs">
              Spec file
            </Label>
            <Input
              id={fileInputID}
              type="file"
              accept=".yaml,.yml,.json,application/yaml,application/json,text/yaml"
              onChange={(e) => {
                const f = e.target.files?.[0] ?? null;
                setFile(f);
                if (f && !specName && !isEditing) {
                  setSpecName(
                    normalizeSpecName(f.name.replace(/\.(ya?ml|json)$/i, "")),
                  );
                }
              }}
              className="py-1"
            />
            <p className="text-xs text-muted-foreground">
              Max 10 MB. YAML or JSON. The server validates the content as
              OpenAPI 3.x before saving.
            </p>
          </TabsContent>

          <TabsContent value="url" className="pt-2">
            <LabeledInput
              label="Spec URL"
              help="HTTPS URL to a publicly reachable OpenAPI document. The server fetches once at save and stores the content; click Refresh on the spec row to re-fetch."
              value={sourceURL}
              onChange={setSourceURL}
              placeholder="https://petstore3.swagger.io/api/v3/openapi.json"
              mono
            />
          </TabsContent>
        </Tabs>

        <LabeledInput
          label="Base path (optional)"
          help="URL path segment prepended to every operation in this spec at invoke time. Set this when the spec ships without a servers[] entry, or when you need to override the spec author's value (sandbox, proxy, version pin). When empty, the toolkit derives the prefix from the spec's first servers[].url. Must start with '/'. Example: /v1 or /api/v2."
          value={basePath}
          onChange={setBasePath}
          placeholder="/v1"
          mono
        />

        <LabeledInput
          label="Title (optional)"
          help="Short label for this spec shown in api_list_specs and the multi-spec gate on api_list_endpoints, so the agent can pick the right section. When empty, the toolkit derives it from the spec's info.title. Set this to override an unhelpful title or give a deployment-specific name. Max 200 characters."
          value={title}
          onChange={setTitle}
          placeholder="Orders API"
        />

        <LabeledTextarea
          label="Description (optional)"
          help="One- or two-sentence summary shown alongside the title in api_list_specs. When empty, the toolkit derives it from the spec's info.description. Set this when the spec ships without a useful description. Max 2000 characters."
          value={description}
          onChange={setDescription}
          placeholder="Create, list, and refund orders."
          rows={3}
        />

        {error && (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </div>
    </ModalShell>
  );
}

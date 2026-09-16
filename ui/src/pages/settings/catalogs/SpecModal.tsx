import { useCallback, useEffect, useId, useState } from "react";
import { AlertCircle, X } from "lucide-react";

import type { APICatalogSpec, APISpecFormat } from "@/api/admin/hooks/catalogs";
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

// What each format means to the operator choosing it, and what the rest of the
// form says once it is chosen. The union itself is APISpecFormat, declared once
// beside the type that carries it; a WSDL is converted to OpenAPI at save time,
// so a document that cannot be imported fails the save here rather than
// registering a connection with no operations.
//
// The labels follow the format because this form is where an operator decides
// what to paste: a box headed "OpenAPI YAML or JSON" above an SDL is the wrong
// instruction, not merely an unhelpful one (#1765).
interface SpecFormatCopy {
  value: APISpecFormat;
  label: string;
  help: string;
  // pasteLabel heads the Paste box, and pastePlaceholder opens a document in
  // this format.
  pasteLabel: string;
  pastePlaceholder: string;
  // uploadAccept filters the file picker and uploadHelp says what the server
  // will read the file as; both are per-format, because a .wsdl and a
  // .graphqls are neither YAML nor JSON.
  uploadAccept: string;
  uploadHelp: string;
  // servesGateway is false for a format the HTTP API gateway never serves,
  // which is what makes the base path meaningless for it: nothing joins a path
  // prefix onto a GraphQL operation.
  servesGateway: boolean;
}

// OPENAPI_FORMAT is the default, and the fallback for a spec_format the
// backend adds before this form knows it.
const OPENAPI_FORMAT: SpecFormatCopy = {
  value: "openapi",
  label: "OpenAPI",
  help: "An OpenAPI 3.x document, in JSON or YAML.",
  pasteLabel: "OpenAPI YAML or JSON",
  pastePlaceholder: "openapi: 3.0.0\ninfo:\n  title: Vendor\n...",
  uploadAccept: ".yaml,.yml,.json,application/yaml,application/json,text/yaml",
  uploadHelp:
    "Max 10 MB. YAML or JSON. The server validates the content as OpenAPI 3.x before saving.",
  servesGateway: true,
};

const SPEC_FORMATS: SpecFormatCopy[] = [
  OPENAPI_FORMAT,
  {
    value: "wsdl",
    label: "WSDL (SOAP)",
    help: "A WSDL 1.1 document/literal service description, SOAP 1.1 or 1.2. Each operation becomes a discoverable operation and the gateway builds the SOAP envelope, so a caller sends the operation's fields rather than XML. RPC and encoded bindings are not imported.",
    pasteLabel: "WSDL",
    pastePlaceholder: '<?xml version="1.0"?>\n<wsdl:definitions ...>\n...',
    uploadAccept: ".wsdl,.xml,application/xml,text/xml",
    uploadHelp:
      "Max 10 MB. A WSDL 1.1 document. The server imports it to OpenAPI before saving; one it cannot import is refused here rather than registering a connection with no operations.",
    servesGateway: true,
  },
  {
    value: "graphql",
    label: "GraphQL (SDL)",
    help: "A GraphQL schema in SDL. It is served to graphql connections that reference this catalog, not to the HTTP API gateway, so an endpoint that disables introspection gets its schema here and several connections against one endpoint share it. graphql_export writes SDL, as does a schema registry.",
    pasteLabel: "GraphQL SDL",
    pastePlaceholder: "type Query {\n  order(id: ID!): Order\n}\n...",
    uploadAccept: ".graphql,.graphqls,.gql,.sdl,text/plain",
    uploadHelp:
      "Max 10 MB. A schema in SDL. The server parses it before saving; an introspection result is refused, since graphql_export and a schema registry both write SDL.",
    servesGateway: false,
  },
];

// sourceTabOf is the tab a stored spec opens on: where its content came from.
// A source kind this form does not author -- `embedded`, the built-in
// platform-admin catalog's -- opens on Paste, showing what is stored.
function sourceTabOf(kind: APICatalogSpec["source_kind"] | undefined): SourceTab {
  if (kind === "url") return "url";
  if (kind === "upload") return "upload";
  return "paste";
}

// stored is a stored spec's optional field as the form holds it: a string,
// never null, so the inputs stay controlled.
function stored(value: string | null | undefined): string {
  return value ?? "";
}

// formatCopy is the entry for the chosen format, falling back to OpenAPI's so
// a value the backend adds later renders the default form rather than nothing.
function formatCopy(format: APISpecFormat): SpecFormatCopy {
  return SPEC_FORMATS.find((f) => f.value === format) ?? OPENAPI_FORMAT;
}

// specFileSuffix is the extension stripped from an uploaded file's name when
// it seeds the spec name. It covers every format's extensions rather than the
// chosen one's, since the name is a label either way and a file named
// orders.graphqls should not seed a spec called "orders.graphqls".
const specFileSuffix = /\.(ya?ml|json|wsdl|xml|graphqls?|gql|sdl)$/i;

// maxUploadBytes mirrors catalogSpecMaxUploadBytes on the upload route, so an
// oversized file is refused here rather than after the upload has been sent.
const maxUploadBytes = 10 * 1024 * 1024;

// specProblem is what stops the save, empty when nothing does. It is a
// function rather than three checks inside the handler so the handler reads as
// validate-then-save.
function specProblem(specName: string, tab: SourceTab, file: File | null): string | null {
  if (!specName) return "spec name is required";
  if (tab !== "upload") return null;
  if (!file) return "choose a file";
  if (file.size > maxUploadBytes) return "file exceeds 10 MB limit";
  return null;
}

// SpecDraft is what the modal has collected, in the shape the two mutations
// take. The tab decides which of content / sourceURL / file is the one that
// matters; the rest travel unused, which is what lets an operator switch tabs
// without losing what they typed in another.
interface SpecDraft {
  catalogID: string;
  specName: string;
  tab: SourceTab;
  specFormat: APISpecFormat;
  content: string;
  sourceURL: string;
  file: File | null;
  base_path: string;
  title: string;
  description: string;
}

// saveSpec sends the draft through whichever route the source tab selected.
async function saveSpec(
  draft: SpecDraft,
  upsert: ReturnType<typeof useUpsertAPICatalogSpec>,
  upload: ReturnType<typeof useUploadAPICatalogSpec>,
): Promise<void> {
  const { catalogID, specName, specFormat, base_path, title, description } = draft;
  const common = { catalogID, specName, spec_format: specFormat, base_path, title, description };
  if (draft.tab === "upload") {
    await upload.mutateAsync({ ...common, file: draft.file as File });
    return;
  }
  if (draft.tab === "url") {
    await upsert.mutateAsync({ ...common, source_kind: "url", source_url: draft.sourceURL });
    return;
  }
  await upsert.mutateAsync({ ...common, source_kind: "inline", content: draft.content });
}

// SpecFormatPicker chooses what the operator is supplying, above the tabs that
// choose where it comes from.
function SpecFormatPicker({
  value,
  onChange,
}: {
  value: APISpecFormat;
  onChange: (f: APISpecFormat) => void;
}) {
  return (
    <fieldset className="space-y-1.5">
      <legend className="text-sm font-medium">Format</legend>
      <div className="flex gap-2">
        {SPEC_FORMATS.map((f) => (
          <Button
            key={f.value}
            type="button"
            size="sm"
            variant={value === f.value ? "default" : "outline"}
            aria-pressed={value === f.value}
            onClick={() => onChange(f.value)}
          >
            {f.label}
          </Button>
        ))}
      </div>
      <p className="text-muted-foreground text-xs">{formatCopy(value).help}</p>
    </fieldset>
  );
}

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
  const [specFormat, setSpecFormat] = useState<APISpecFormat>("openapi");
  const [content, setContent] = useState("");
  const [sourceURL, setSourceURL] = useState("");
  const [basePath, setBasePath] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState<string | null>(null);
  const fileInputID = useId();
  const copy = formatCopy(specFormat);

  useEffect(() => {
    if (!existing) return;
    const opensOn = sourceTabOf(existing.source_kind);
    setTab(opensOn);
    setContent(opensOn === "url" ? "" : stored(existing.content));
    setSourceURL(stored(existing.source_url));
    setSpecFormat(existing.spec_format ?? "openapi");
    setBasePath(stored(existing.base_path));
    setTitle(stored(existing.title));
    setDescription(stored(existing.description));
  }, [existing]);

  const submit = useCallback(async () => {
    setError(null);
    const problem = specProblem(specName, tab, file);
    if (problem) {
      setError(problem);
      return;
    }
    try {
      await saveSpec(
        {
          catalogID,
          specName,
          tab,
          specFormat,
          content,
          sourceURL,
          file,
          base_path: basePath.trim(),
          title: title.trim(),
          description: description.trim(),
        },
        upsert,
        upload,
      );
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    }
  }, [
    catalogID,
    specName,
    tab,
    specFormat,
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
            "A short label for this component within the catalog. Use 'default' if the catalog has one spec. Use multiple names (e.g. drive, gmail) only when the catalog bundles separate APIs; the model sees this label in the spec field of api_discover so it can pick the right operation. Lowercase letters, digits, hyphens, or underscores; typed input is auto-lowercased."
          }
          value={specName}
          onChange={(v) => setSpecName(normalizeSpecName(v))}
          mono
          disabled={isEditing}
          placeholder="default"
        />

        <SpecFormatPicker value={specFormat} onChange={setSpecFormat} />

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
              label={copy.pasteLabel}
              value={content}
              onChange={setContent}
              placeholder={copy.pastePlaceholder}
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
              accept={copy.uploadAccept}
              onChange={(e) => {
                const f = e.target.files?.[0] ?? null;
                setFile(f);
                if (f && !specName && !isEditing) {
                  setSpecName(normalizeSpecName(f.name.replace(specFileSuffix, "")));
                }
              }}
              className="py-1"
            />
            <p className="text-xs text-muted-foreground">{copy.uploadHelp}</p>
          </TabsContent>

          <TabsContent value="url" className="pt-2">
            <LabeledInput
              label="Spec URL"
              help="HTTPS URL to a publicly reachable document in the format selected above — an OpenAPI document, a WSDL (often the service address with ?wsdl), or a GraphQL schema in SDL. The server fetches once at save and stores the content; click Refresh on the spec row to re-fetch and re-import."
              value={sourceURL}
              onChange={setSourceURL}
              placeholder="https://petstore3.swagger.io/api/v3/openapi.json"
              mono
            />
          </TabsContent>
        </Tabs>

        {/* The base path, title and description are the HTTP gateway's: a path
            prefix joined onto an operation, and the labels api_discover shows
            at its specs level. A format the gateway never serves reads none of
            the three, so the form says so rather than offering fields whose
            values nothing will read. What an earlier save stored is kept: the
            draft still carries it and Save still sends it. */}
        {copy.servesGateway ? (
          <>
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
              help="Short label for this spec shown at api_discover's specs level, so the agent can pick the right section. When empty, the toolkit derives it from the spec's info.title. Set this to override an unhelpful title or give a deployment-specific name. Max 200 characters."
              value={title}
              onChange={setTitle}
              placeholder="Orders API"
            />

            <LabeledTextarea
              label="Description (optional)"
              help="One- or two-sentence summary shown alongside the title at api_discover's specs level. When empty, the toolkit derives it from the spec's info.description. Set this when the spec ships without a useful description. Max 2000 characters."
              value={description}
              onChange={setDescription}
              placeholder="Create, list, and refund orders."
              rows={3}
            />
          </>
        ) : (
          <p className="text-muted-foreground text-xs">
            Base path, title and description are not read for this format: the
            schema is served to the graphql connections that reference this
            catalog, which read the document itself.
          </p>
        )}

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

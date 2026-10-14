import { useState } from "react";
import { ArrowDownRight, ArrowUpLeft, BookMarked, Building2, FileCode, Settings2, Tag, Users } from "lucide-react";
import { useDataset, type Column, type Dataset, type Hop } from "@/api/portal/catalog";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { catalogHref, columnRef, parseRef } from "@/lib/entityRefs";
import { ContextDocuments } from "./ContextDocuments";
import { DefinitionChips, DomainEditor, OwnersEditor, PropertiesEditor } from "./editors";
import { BackToList, EntityDescription } from "./governance";
import { HistoryPanel } from "./HistoryPanel";
import { ListSkeleton } from "./primitives";
import { SchemaDocumentPanel } from "./SchemaDocumentPanel";
import { refLabel } from "./utils";

/** DatasetDetail is one dataset's page: its curation, its columns and theirs,
 * the notes attached to it, the lineage around it and the history of every
 * change written to it. */
export function DatasetDetail({
  reference,
  onBack,
  onNavigate,
}: {
  reference: string;
  onBack: () => void;
  onNavigate?: (path: string) => void;
}) {
  const { data, isLoading, isError } = useDataset(reference);
  return (
    <div className="space-y-4">
      <BackToList label="Back to tables" onBack={onBack} />
      {data ? (
        <DatasetBody dataset={data} onNavigate={onNavigate} />
      ) : isLoading ? (
        <ListSkeleton />
      ) : (
        <p className="text-sm text-destructive">
          {isError ? "This dataset could not be opened." : "Dataset not found."}
        </p>
      )}
    </div>
  );
}

// connectionOf is the connection a dataset reference names, as "name (kind)".
function connectionOf(ref: string): string {
  const m = ref.match(/^mcp:dataset:\(([^,]+),([^,]+),/);
  return m ? `${m[2]} (${m[1]})` : "";
}

// sectionTitle is a section heading with its icon.
function sectionTitle(I: typeof Tag, label: string) {
  return (
    <span className="flex items-center gap-1.5">
      <I className="size-4" /> {label}
    </span>
  );
}

function DatasetBody({
  dataset: d,
  onNavigate,
}: {
  dataset: Dataset;
  onNavigate?: (path: string) => void;
}) {
  const a = d.aspects ?? {};
  const canEdit = d.writable;
  return (
    <div className="space-y-6">
      <DatasetHeader dataset={d} />

      <EntityDescription
        reference={d.reference}
        value={a.description ?? ""}
        canEdit={canEdit}
        label="Dataset description"
      />

      <CurationGrid dataset={d} />

      <SectionCard title={sectionTitle(Settings2, "Properties")}>
        <PropertiesEditor
          reference={d.reference}
          read={a.custom_properties}
          canEdit={canEdit}
        />
      </SectionCard>

      <ColumnsSection dataset={d} canEdit={canEdit} />

      <ContextDocuments
        reference={d.reference}
        read={a.context_documents}
        canEdit={canEdit}
      />

      <LineageSection upstream={d.upstream ?? []} downstream={d.downstream ?? []} onNavigate={onNavigate} />

      {d.schema_document && (
        <SectionCard title={sectionTitle(FileCode, "Schema document")}>
          <SchemaDocumentPanel reference={d.schema_document} />
        </SectionCard>
      )}

      <KnowledgeBacklinks urn={d.reference} onNavigate={onNavigate} />

      <HistoryPanel reference={d.reference} canEdit={canEdit} />
    </div>
  );
}

function DatasetHeader({ dataset: d }: { dataset: Dataset }) {
  return (
    <div className="space-y-1">
      <h2 className="text-lg font-semibold break-all">{d.name}</h2>
      <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{connectionOf(d.reference)}</span>
        <Badge variant="muted">{d.type}</Badge>
        {d.removed_at && <Badge variant="warning">No longer seen by its connection</Badge>}
        <code className="break-all">{d.reference}</code>
      </p>
    </div>
  );
}

// CurationGrid is a dataset's tags, glossary terms, owners and domain.
function CurationGrid({ dataset: d }: { dataset: Dataset }) {
  const a = d.aspects ?? {};
  const canEdit = d.writable;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <SectionCard title={sectionTitle(Tag, "Tags")}>
        <DefinitionChips
          reference={d.reference}
          aspect="tags"
          kind="tag"
          read={a.tags}
          canEdit={canEdit}
          what="tag"
        />
      </SectionCard>
      <SectionCard title={sectionTitle(BookMarked, "Glossary terms")}>
        <DefinitionChips
          reference={d.reference}
          aspect="glossary_terms"
          kind="glossary_term"
          read={a.glossary_terms}
          names={d.names}
          canEdit={canEdit}
          what="glossary term"
        />
      </SectionCard>
      <SectionCard title={sectionTitle(Users, "Owners")}>
        <OwnersEditor reference={d.reference} read={a.owners} canEdit={canEdit} />
      </SectionCard>
      <SectionCard title={sectionTitle(Building2, "Domain")}>
        <DomainEditor reference={d.reference} value={a.domain ?? ""} names={d.names} canEdit={canEdit} />
      </SectionCard>
    </div>
  );
}

function ColumnsSection({ dataset: d, canEdit }: { dataset: Dataset; canEdit: boolean }) {
  const [open, setOpen] = useState<string | null>(null);
  const columns = [...(d.columns ?? [])].sort((x, y) => x.ordinal - y.ordinal);
  if (columns.length === 0) return null;
  return (
    <SectionCard title="Columns">
      <div className="overflow-x-auto rounded-lg border">
        <Table>
          <TableHeader>
            <TableRow className="bg-muted/50 hover:bg-muted/50">
              <TableHead className="px-3 text-xs text-muted-foreground">Name</TableHead>
              <TableHead className="px-3 text-xs text-muted-foreground">Type</TableHead>
              <TableHead className="px-3 text-xs text-muted-foreground">Description</TableHead>
              <TableHead className="px-3 text-xs text-muted-foreground">Tags and terms</TableHead>
              <TableHead className="px-3" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {columns.map((c) => (
              <ColumnRow
                key={c.name}
                dataset={d}
                column={c}
                canEdit={canEdit}
                open={open === c.name}
                onToggle={() => setOpen(open === c.name ? null : c.name)}
              />
            ))}
          </TableBody>
        </Table>
      </div>
    </SectionCard>
  );
}

function ColumnRow({
  dataset: d,
  column: c,
  canEdit,
  open,
  onToggle,
}: {
  dataset: Dataset;
  column: Column;
  canEdit: boolean;
  open: boolean;
  onToggle: () => void;
}) {
  const a = c.aspects ?? {};
  return (
    <>
      <TableRow className="align-top">
        <TableCell className="px-3 py-2 align-top font-mono text-xs">{c.name}</TableCell>
        <TableCell className="px-3 py-2 align-top font-mono text-xs text-muted-foreground">
          {c.data_type}
        </TableCell>
        <TableCell className="px-3 py-2 align-top whitespace-normal text-muted-foreground">
          {a.description ? (
            // max-width on a <td> is ignored in auto table layout, so the
            // description column is bounded on an inner block.
            <div className="max-w-md">
              <CollapsibleMarkdown content={a.description} fadeFrom="from-card" />
            </div>
          ) : (
            "—"
          )}
        </TableCell>
        <TableCell className="px-3 py-2 align-top">
          <span className="flex flex-wrap gap-1">
            {(a.tags ?? []).map((t) => (
              <Badge key={t} variant="info">
                {t}
              </Badge>
            ))}
            {(a.glossary_terms ?? []).map((g) => (
              <Badge key={g} variant="muted">
                {d.names?.[g] ?? refLabel(g)}
              </Badge>
            ))}
          </span>
        </TableCell>
        <TableCell className="px-3 py-2 text-right align-top">
          {canEdit && (
            <Button variant="ghost" size="xs" onClick={onToggle} aria-label={`Edit column ${c.name}`}>
              {open ? "Close" : "Edit"}
            </Button>
          )}
        </TableCell>
      </TableRow>
      {open && <ColumnEditor dataset={d} column={c} canEdit={canEdit} />}
    </>
  );
}

// ColumnEditor is a column's curation opened for editing, under its row.
function ColumnEditor({ dataset: d, column: c, canEdit }: { dataset: Dataset; column: Column; canEdit: boolean }) {
  const a = c.aspects ?? {};
  const ref = columnRef(d.reference, c.name);
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell colSpan={5} className="space-y-3 bg-muted/30 px-3 py-3 whitespace-normal">
        <EntityDescription
          reference={ref}
          value={a.description ?? ""}
          canEdit={canEdit}
          label={`Description of ${c.name}`}
        />
        <div className="grid gap-3 lg:grid-cols-2">
          <SectionCard title="Tags">
            <DefinitionChips
              reference={ref}
              aspect="tags"
              kind="tag"
              read={a.tags}
              canEdit={canEdit}
              what={`tag on ${c.name}`}
            />
          </SectionCard>
          <SectionCard title="Glossary terms">
            <DefinitionChips
              reference={ref}
              aspect="glossary_terms"
              kind="glossary_term"
              read={a.glossary_terms}
              names={d.names}
              canEdit={canEdit}
              what={`glossary term on ${c.name}`}
            />
          </SectionCard>
        </div>
        <HistoryPanel reference={ref} canEdit={canEdit} />
      </TableCell>
    </TableRow>
  );
}

function LineageSection({
  upstream,
  downstream,
  onNavigate,
}: {
  upstream: Hop[];
  downstream: Hop[];
  onNavigate?: (path: string) => void;
}) {
  if (upstream.length === 0 && downstream.length === 0) return null;
  return (
    <SectionCard title="Lineage">
      <div className="grid gap-4 md:grid-cols-2">
        <HopList
          title="Upstream"
          icon={<ArrowUpLeft className="size-4" />}
          refs={upstream.map((h) => ({ ref: h.source, depth: h.depth }))}
          onNavigate={onNavigate}
        />
        <HopList
          title="Downstream"
          icon={<ArrowDownRight className="size-4" />}
          refs={downstream.map((h) => ({ ref: h.target, depth: h.depth }))}
          onNavigate={onNavigate}
        />
      </div>
    </SectionCard>
  );
}

function HopList({
  title,
  icon,
  refs,
  onNavigate,
}: {
  title: string;
  icon: React.ReactNode;
  refs: { ref: string; depth: number }[];
  onNavigate?: (path: string) => void;
}) {
  // A dataset reached by two paths is listed once, at its nearest depth.
  const seen = new Map<string, number>();
  for (const r of refs) seen.set(r.ref, Math.min(seen.get(r.ref) ?? r.depth, r.depth));
  const rows = [...seen.entries()].sort((x, y) => x[1] - y[1]);
  return (
    <div>
      <h4 className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        {icon} {title}
      </h4>
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">None.</p>
      ) : (
        <ul className="space-y-1">
          {rows.map(([ref, depth]) => {
            const href = catalogHref(ref);
            const label = parseRef(ref)?.fallbackLabel ?? ref;
            return (
              <li key={ref} className="flex items-center gap-2 text-sm">
                {href && onNavigate ? (
                  <a
                    href={href}
                    title={ref}
                    onClick={(e) => {
                      e.preventDefault();
                      onNavigate(href);
                    }}
                    className="text-primary hover:underline"
                  >
                    {label}
                  </a>
                ) : (
                  <span title={ref}>{label}</span>
                )}
                {depth > 1 && <span className="text-xs text-muted-foreground">{depth} hops</span>}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

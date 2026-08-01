import { useState } from "react";
import { Tag, Users, BookMarked, Building2, X } from "lucide-react";
import {
  useUpdateDescription,
  useUpdateTags,
  useUpdateOwners,
  useUpdateGlossaryTerms,
  useUpdateDomain,
  useTagLookup,
  useGlossaryLookup,
  useDomainLookup,
  type CatalogEntity,
  type EntityRef,
} from "@/api/portal/datahub";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { useDebounced } from "@/lib/useDebounced";
import { shortUrn, filterDomains, withRawUrn } from "./utils";
import {
  Badge,
  EditButton,
  SaveButton,
  CancelButton,
  AddButton,
  MutationError,
} from "./primitives";
import { MetadataPicker } from "./MetadataPicker";

export function EntityBody({
  conn,
  entity,
  canEdit,
}: {
  conn: string;
  entity: CatalogEntity;
  canEdit: boolean;
}) {
  const ctx = entity.context ?? {};
  const columns = Object.values(entity.columns ?? {});
  return (
    <div className="space-y-6">
      <div>
        <h2 className="break-all text-lg font-semibold">{ctx.urn ?? entity.urn}</h2>
      </div>

      <DescriptionEditor conn={conn} urn={entity.urn} value={ctx.description ?? ""} canEdit={canEdit} />

      <ChipSetSection
        icon={<Tag className="h-4 w-4" />}
        title="Tags"
        conn={conn}
        urn={entity.urn}
        values={(ctx.tag_refs ?? []).map((t) => ({ key: t.urn, label: t.name || shortUrn(t.urn) }))}
        canEdit={canEdit}
        kind="tags"
        placeholder="Search tags by name…"
      />

      <ChipSetSection
        icon={<BookMarked className="h-4 w-4" />}
        title="Glossary terms"
        conn={conn}
        urn={entity.urn}
        values={(ctx.glossary_terms ?? []).map((g) => ({ key: g.urn, label: g.name || shortUrn(g.urn) }))}
        canEdit={canEdit}
        kind="glossary"
        placeholder="Search glossary terms by name…"
      />

      <OwnersSection conn={conn} urn={entity.urn} owners={ctx.owners ?? []} canEdit={canEdit} />

      <DomainSection conn={conn} urn={entity.urn} domain={ctx.domain ?? null} canEdit={canEdit} />

      {columns.length > 0 && (
        <section>
          <h3 className="mb-2 text-sm font-semibold">Columns</h3>
          <div className="overflow-hidden rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">Name</th>
                  <th className="px-3 py-2 font-medium">Description</th>
                  <th className="px-3 py-2 font-medium">Classification</th>
                </tr>
              </thead>
              <tbody>
                {columns.map((c) => (
                  <tr key={c.name} className="border-t align-top">
                    <td className="px-3 py-2 font-mono text-xs">{c.name}</td>
                    <td className="px-3 py-2 text-muted-foreground">
                      {c.description ? (
                        // max-width on a <td> is ignored in auto table layout, so
                        // the description column is bounded on an inner block.
                        <div className="max-w-md">
                          <CollapsibleMarkdown content={c.description} fadeFrom="from-background" />
                        </div>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="px-3 py-2">
                      <span className="flex flex-wrap gap-1">
                        {c.is_pii && <Badge tone="amber">PII</Badge>}
                        {c.is_sensitive && <Badge tone="amber">Sensitive</Badge>}
                        {(c.tags ?? []).map((t) => (
                          <Badge key={t} tone="primary">
                            {shortUrn(t)}
                          </Badge>
                        ))}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

function DescriptionEditor({
  conn,
  urn,
  value,
  canEdit,
}: {
  conn: string;
  urn: string;
  value: string;
  canEdit: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const mut = useUpdateDescription(conn);

  return (
    <section>
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm font-semibold">Description</h3>
        {canEdit && !editing && (
          <EditButton
            onClick={() => {
              setDraft(value);
              setEditing(true);
            }}
          />
        )}
      </div>
      {editing ? (
        <div className="space-y-2">
          <MarkdownEditor
            value={draft}
            onChange={setDraft}
            minHeight="240px"
            placeholder="Describe this dataset in markdown…"
          />
          <MutationError mut={mut} />
          <div className="flex gap-2">
            <SaveButton
              disabled={mut.isPending}
              onClick={() =>
                mut.mutate({ urn, description: draft }, { onSuccess: () => setEditing(false) })
              }
            />
            <CancelButton onClick={() => setEditing(false)} />
          </div>
        </div>
      ) : value ? (
        <MarkdownRenderer content={value} bare />
      ) : (
        <p className="text-sm text-muted-foreground">No description.</p>
      )}
    </section>
  );
}

type ChipKind = "tags" | "glossary";

function ChipSetSection({
  icon,
  title,
  conn,
  urn,
  values,
  canEdit,
  kind,
  placeholder,
}: {
  icon: React.ReactNode;
  title: string;
  conn: string;
  urn: string;
  values: { key: string; label: string }[];
  canEdit: boolean;
  kind: ChipKind;
  placeholder: string;
}) {
  const [query, setQuery] = useState("");
  const tags = useUpdateTags(conn);
  const glossary = useUpdateGlossaryTerms(conn);
  const mut = kind === "tags" ? tags : glossary;

  // Only the active kind's lookup runs; the other gets an empty query (disabled).
  const debounced = useDebounced(query, 250);
  const tagLookup = useTagLookup(conn, kind === "tags" ? debounced : "");
  const glossaryLookup = useGlossaryLookup(conn, kind === "glossary" ? debounced : "");
  const lookup = kind === "tags" ? tagLookup : glossaryLookup;
  const existing = new Set(values.map((v) => v.key));

  // Power-user fallback: an exact, well-formed URN typed into the box is offered as
  // a candidate so a value that name search does not surface (e.g. a brand-new tag)
  // can still be applied without a raw free-text field (#785 review).
  const urnType = kind === "tags" ? "tag" : "glossaryTerm";
  const candidates = withRawUrn(lookup.data ?? [], query, urnType);

  const pick = (ref: EntityRef) => {
    if (existing.has(ref.urn)) return;
    mut.mutate({ urn, add: [ref.urn] }, { onSuccess: () => setQuery("") });
  };

  return (
    <section>
      <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
        {icon} {title}
      </h3>
      <div className="flex flex-wrap items-center gap-1.5">
        {values.length === 0 && <span className="text-sm text-muted-foreground">None.</span>}
        {values.map((v) => (
          <span
            key={v.key}
            className="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary"
          >
            {v.label}
            {canEdit && (
              <button
                aria-label={`Remove ${v.label}`}
                onClick={() => mut.mutate({ urn, remove: [v.key] })}
                className="rounded-full hover:bg-primary/20"
              >
                <X className="h-3 w-3" />
              </button>
            )}
          </span>
        ))}
      </div>
      {canEdit && (
        <MetadataPicker
          placeholder={placeholder}
          query={query}
          setQuery={setQuery}
          candidates={candidates}
          loading={lookup.isFetching || (lookup.data === undefined && !lookup.isError)}
          isPending={mut.isPending}
          existingKeys={existing}
          onPick={pick}
          emptyHint={lookup.isError ? "Lookup failed." : "No matches."}
        />
      )}
      <MutationError mut={mut} />
    </section>
  );
}

function OwnersSection({
  conn,
  urn,
  owners,
  canEdit,
}: {
  conn: string;
  urn: string;
  owners: { urn: string; name?: string; email?: string; type: string }[];
  canEdit: boolean;
}) {
  const [ownerUrn, setOwnerUrn] = useState("");
  const [ownerType, setOwnerType] = useState("TECHNICAL_OWNER");
  const mut = useUpdateOwners(conn);

  return (
    <section>
      <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
        <Users className="h-4 w-4" /> Owners
      </h3>
      <div className="space-y-1">
        {owners.length === 0 && <span className="text-sm text-muted-foreground">None.</span>}
        {owners.map((o) => (
          <div key={o.urn} className="flex items-center gap-2 text-sm">
            <span>{o.name || o.email || shortUrn(o.urn)}</span>
            <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{o.type}</span>
            {canEdit && (
              <button
                aria-label={`Remove owner ${o.urn}`}
                onClick={() => mut.mutate({ urn, remove: [o.urn] })}
                className="text-muted-foreground hover:text-destructive"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        ))}
      </div>
      {canEdit && (
        <div className="mt-2 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={ownerUrn}
              onChange={(e) => setOwnerUrn(e.target.value)}
              placeholder="urn:li:corpuser:alice"
              className="w-64 rounded-md border bg-background px-2 py-1 font-mono text-xs outline-none ring-ring focus:ring-2"
            />
            <select
              value={ownerType}
              onChange={(e) => setOwnerType(e.target.value)}
              className="rounded-md border bg-background px-2 py-1 text-xs outline-none ring-ring focus:ring-2"
            >
              <option>TECHNICAL_OWNER</option>
              <option>BUSINESS_OWNER</option>
              <option>DATA_STEWARD</option>
            </select>
            <AddButton
              disabled={mut.isPending || !ownerUrn.trim()}
              onClick={() =>
                mut.mutate(
                  { urn, add_owners: [{ owner_urn: ownerUrn.trim(), ownership_type: ownerType }] },
                  { onSuccess: () => setOwnerUrn("") },
                )
              }
            />
          </div>
          <p className="text-[11px] text-muted-foreground">
            Enter a DataHub user or group URN, e.g. <code>urn:li:corpuser:alice</code> or{" "}
            <code>urn:li:corpGroup:data-eng</code>.
          </p>
        </div>
      )}
      <MutationError mut={mut} />
    </section>
  );
}

function DomainSection({
  conn,
  urn,
  domain,
  canEdit,
}: {
  conn: string;
  urn: string;
  domain: { urn: string; name: string } | null;
  canEdit: boolean;
}) {
  const [query, setQuery] = useState("");
  const mut = useUpdateDomain(conn);
  // Domains have no name-scoped search upstream, so load the full list and filter
  // client-side. Fetch only when the editor can edit.
  const lookup = useDomainLookup(conn, canEdit);
  // The upstream domain list is capped (100), so a domain beyond it would be
  // unreachable; an exact urn:li:domain URN typed into the box is offered as a
  // fallback candidate so any domain can still be set (#785 review).
  const candidates = withRawUrn(filterDomains(lookup.data ?? [], query), query, "domain");

  const pick = (ref: EntityRef) => {
    mut.mutate({ urn, domain: ref.urn }, { onSuccess: () => setQuery("") });
  };

  return (
    <section>
      <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
        <Building2 className="h-4 w-4" /> Domain
      </h3>
      <div className="flex items-center gap-2 text-sm">
        {domain ? (
          <>
            <span>{domain.name || shortUrn(domain.urn)}</span>
            {canEdit && (
              <button
                onClick={() => mut.mutate({ urn, clear_domain: true })}
                className="text-xs text-muted-foreground hover:text-destructive"
              >
                Clear
              </button>
            )}
          </>
        ) : (
          <span className="text-muted-foreground">None.</span>
        )}
      </div>
      {canEdit && (
        <MetadataPicker
          placeholder="Search domains by name…"
          query={query}
          setQuery={setQuery}
          candidates={candidates}
          loading={lookup.isFetching || (lookup.data === undefined && !lookup.isError)}
          isPending={mut.isPending}
          existingKeys={new Set(domain ? [domain.urn] : [])}
          onPick={pick}
          openOnFocus
          emptyHint={lookup.isError ? "Failed to load domains." : "No domains match."}
        />
      )}
      <MutationError mut={mut} />
    </section>
  );
}

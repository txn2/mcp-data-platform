import { useDataset, useDefinition, type Aspects } from "@/api/portal/catalog";
import { ApiError } from "@/api/portal/client";
import { Badge } from "@/components/ui/badge";
import { catalogKind, columnDataset } from "@/lib/entityRefs";
import { cn } from "@/lib/utils";

/**
 * CatalogNodeDetail resolves a catalog node against the platform catalog.
 *
 * A graph node's label for a catalog reference is DERIVED from the reference
 * (the table its path ends in, the key a definition carries), not read from the
 * catalog, so it is not the entity's name. This looks the entity up and shows
 * what the catalog actually holds, or says plainly that the citation points at
 * something the catalog does not have. Resolution happens only for the node the
 * reader has selected, so the corpus-wide graph read stays one request.
 */
export function CatalogNodeDetail({ urn }: { urn: string }) {
  const kind = catalogKind(urn);
  if (!kind || kind === "schema_document") return <CatalogRow>A catalog schema document.</CatalogRow>;
  return kind === "dataset" || kind === "column" ? (
    <DatasetFacts reference={columnDataset(urn)} />
  ) : (
    <DefinitionFacts reference={urn} />
  );
}

function DatasetFacts({ reference }: { reference: string }) {
  const q = useDataset(reference);
  if (q.isLoading) return <CatalogRow>Looking this up in the catalog...</CatalogRow>;
  if (q.isError || !q.data) return <LookupFailure error={q.error} />;
  return <CatalogFacts aspects={q.data.aspects ?? {}} names={q.data.names} />;
}

function DefinitionFacts({ reference }: { reference: string }) {
  const q = useDefinition(reference);
  if (q.isLoading) return <CatalogRow>Looking this up in the catalog...</CatalogRow>;
  if (q.isError || !q.data) return <LookupFailure error={q.error} />;
  return <CatalogFacts aspects={q.data.aspects ?? {}} />;
}

/**
 * LookupFailure separates a catalog that does not hold the entity from one that
 * could not be read. The catalog reports the first as a 404 (#1610).
 */
function LookupFailure({ error }: { error: unknown }) {
  if (error instanceof ApiError && error.status === 404) {
    return (
      <CatalogRow tone="warn">
        Not in the catalog. A page cites this entity, but the catalog does not have it.
      </CatalogRow>
    );
  }
  return <CatalogRow>Could not read the catalog.</CatalogRow>;
}

/** isDocumented reports whether anyone has described the entity. */
function isDocumented(a: Aspects): boolean {
  return Boolean(a.description || a.owners?.length || a.tags?.length || a.glossary_terms?.length || a.domain);
}

/** CatalogFacts renders what the catalog holds for a resolved entity. */
function CatalogFacts({ aspects: a, names }: { aspects: Aspects; names?: Record<string, string> }) {
  const owners = a.owners?.length ?? 0;
  return (
    <div className="rounded-md border border-border bg-muted/40 px-2.5 py-2 text-xs">
      <p className="mb-1 text-muted-foreground">In the catalog</p>
      {!isDocumented(a) && (
        <p className="text-foreground">
          The catalog holds this with no description, owners or tags recorded.
        </p>
      )}
      {a.description && <p className="line-clamp-4 text-foreground">{a.description}</p>}
      {a.domain && (
        <p className="mt-1 text-muted-foreground">Domain: {names?.[a.domain] ?? a.domain}</p>
      )}
      {owners > 0 && (
        <p className="mt-1 text-muted-foreground">
          {owners} owner{owners === 1 ? "" : "s"}
        </p>
      )}
      <CatalogTags tags={a.tags} />
    </div>
  );
}

/** CatalogTags renders the tags the catalog carries for an entity, if any. */
function CatalogTags({ tags }: { tags?: string[] }) {
  if (!tags?.length) return null;
  return (
    <p className="mt-1 flex flex-wrap gap-1">
      {tags.map((t) => (
        <Badge key={t} variant="outline" className="rounded-sm px-1 text-muted-foreground">
          {t}
        </Badge>
      ))}
    </p>
  );
}

/**
 * CatalogRow is the one-line frame the lookup's non-result states share. It is
 * a readout rather than an Alert even in its warning tone: it re-renders for
 * every node the reader selects, and ui/alert is `role="alert"`, so each
 * selection would be announced as a new alert.
 */
function CatalogRow({
  children,
  tone = "muted",
}: {
  children: React.ReactNode;
  tone?: "muted" | "warn";
}) {
  return (
    <p
      className={cn(
        "rounded-md border px-2.5 py-2 text-xs text-muted-foreground",
        tone === "warn" ? "border-destructive/40 bg-destructive/5" : "bg-muted/40",
      )}
    >
      {children}
    </p>
  );
}

import {
  useCatalogEntity,
  useDataHubConnections,
  type TableContext,
} from "@/api/portal/datahub";
import { ApiError } from "@/api/portal/client";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * CatalogNodeDetail resolves a catalog node against the DataHub catalog itself.
 *
 * A graph node's label for a catalog reference is DERIVED from its URN (the
 * dataset segment of `urn:li:dataset:(platform,name,env)`), not read from
 * DataHub — so it is not the entity's name, and searching the catalog for it
 * finds nothing. This looks the entity up and shows what the catalog actually
 * holds, or says plainly that the citation points at something the catalog does
 * not have. Resolution happens only for the node the reader has selected, so the
 * corpus-wide graph read stays one request.
 */
export function CatalogNodeDetail({ urn }: { urn: string }) {
  const { data: connections } = useDataHubConnections();
  // Resolve against the first configured connection. Naming it in the readout
  // keeps the claim true on a deployment with several catalogs: "not in acme" is
  // a fact, "not in the catalog" would be a guess about the others.
  const conn = connections?.[0]?.name ?? "";
  const { data, isLoading, isError, error } = useCatalogEntity(conn, urn);

  if (!conn) return <CatalogRow>No DataHub connection is configured.</CatalogRow>;
  if (isLoading) return <CatalogRow>Looking this up in {conn}...</CatalogRow>;
  if (isError || !data?.context) return <LookupFailure conn={conn} error={error} />;
  return <CatalogFacts conn={conn} context={data.context} />;
}

/**
 * LookupFailure separates a catalog that does not hold the entity from one that
 * could not be reached. The catalog reports the first as a 404 (#1610); reading
 * the answer off the record's own fields, as this component did, called an
 * entity nobody had documented missing.
 */
function LookupFailure({ conn, error }: { conn: string; error: unknown }) {
  if (error instanceof ApiError && error.status === 404) {
    return (
      <CatalogRow tone="warn">
        Not found in <span className="font-medium text-foreground">{conn}</span>. A page cites this
        dataset, but the catalog does not have it.
      </CatalogRow>
    );
  }
  return <CatalogRow>Could not reach the {conn} catalog.</CatalogRow>;
}

/** isDocumented reports whether anyone has described the entity the catalog holds. */
function isDocumented(context: TableContext): boolean {
  return Boolean(
    context.description ||
      context.owners?.length ||
      context.tags?.length ||
      context.glossary_terms?.length ||
      context.domain ||
      context.quality_score != null,
  );
}

/** CatalogFacts renders what the catalog holds for a resolved entity. */
function CatalogFacts({ conn, context }: { conn: string; context: TableContext }) {
  const owners = context.owners?.length ?? 0;
  const documented = isDocumented(context);
  return (
    <div className="rounded-md border border-border bg-muted/40 px-2.5 py-2 text-xs">
      <p className="mb-1 text-muted-foreground">In {conn}</p>
      {!documented && (
        <p className="text-foreground">
          The catalog holds this dataset with no description, owners or tags recorded.
        </p>
      )}
      {context.description && <p className="line-clamp-4 text-foreground">{context.description}</p>}
      {context.domain?.name && (
        <p className="mt-1 text-muted-foreground">Domain: {context.domain.name}</p>
      )}
      {owners > 0 && (
        <p className="mt-1 text-muted-foreground">
          {owners} owner{owners === 1 ? "" : "s"}
        </p>
      )}
      <CatalogTags tags={context.tags} />
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

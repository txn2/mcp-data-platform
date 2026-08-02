import {
  useCatalogEntity,
  useDataHubConnections,
  type TableContext,
} from "@/api/portal/datahub";

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
  const { data, isLoading, isError } = useCatalogEntity(conn, urn);

  if (!conn) return <CatalogRow>No DataHub connection is configured.</CatalogRow>;
  if (isLoading) return <CatalogRow>Looking this up in {conn}...</CatalogRow>;
  if (isError) return <CatalogRow>Could not reach the {conn} catalog.</CatalogRow>;

  if (!hasCatalogMetadata(data?.context)) {
    return (
      <CatalogRow tone="warn">
        Not found in <span className="font-medium text-foreground">{conn}</span>. A page cites this
        dataset, but the catalog does not have it.
      </CatalogRow>
    );
  }
  return <CatalogFacts conn={conn} context={data!.context!} />;
}

/**
 * hasCatalogMetadata reports whether the catalog actually holds this entity.
 * DataHub answers for an unknown URN with the URN echoed back and nothing else,
 * so carrying any metadata at all is what separates a real entity from a
 * citation pointing at a dataset this catalog does not have.
 */
function hasCatalogMetadata(context: TableContext | null | undefined): boolean {
  if (!context) return false;
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
  return (
    <div className="rounded-md border border-border bg-muted/40 px-2.5 py-2 text-xs">
      <p className="mb-1 text-muted-foreground">In {conn}</p>
      {context.description && <p className="line-clamp-4 text-foreground">{context.description}</p>}
      {context.domain?.name && (
        <p className="mt-1 text-muted-foreground">Domain: {context.domain.name}</p>
      )}
      {owners > 0 && (
        <p className="mt-1 text-muted-foreground">
          {owners} owner{owners === 1 ? "" : "s"}
        </p>
      )}
      {!!context.tags?.length && (
        <p className="mt-1 flex flex-wrap gap-1">
          {context.tags.map((t) => (
            <span key={t} className="rounded border border-border px-1 text-muted-foreground">
              {t}
            </span>
          ))}
        </p>
      )}
    </div>
  );
}

/** CatalogRow is the one-line frame the lookup's non-result states share. */
function CatalogRow({
  children,
  tone = "muted",
}: {
  children: React.ReactNode;
  tone?: "muted" | "warn";
}) {
  return (
    <p
      className={`rounded-md border px-2.5 py-2 text-xs ${
        tone === "warn"
          ? "border-destructive/40 bg-destructive/5 text-muted-foreground"
          : "border-border bg-muted/40 text-muted-foreground"
      }`}
    >
      {children}
    </p>
  );
}

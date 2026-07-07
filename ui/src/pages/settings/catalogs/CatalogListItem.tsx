import { type APICatalogSummary } from "@/api/admin/hooks";
import { cn } from "@/lib/utils";

// CatalogListItem is one row in the left-nav catalog list. When the
// row belongs to a multi-version group, showVersion=true causes the
// version label to render as an inline chip so the operator can pick
// the right version under the shared slug header. For single-version
// catalogs the version is omitted from the row (it stays visible in
// the editor header) so the list stays uncluttered.
export function CatalogListItem({
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

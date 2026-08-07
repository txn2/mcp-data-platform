import { type APICatalogSummary } from "@/api/admin/hooks";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
    <Button
      variant="ghost"
      onClick={onSelect}
      aria-current={selected ? "true" : undefined}
      // A nav row, not a control: two stacked lines of text, so it sizes to
      // its content instead of the button's fixed height.
      className={cn(
        "h-auto w-full flex-col items-stretch gap-0.5 px-3 py-2 text-left font-normal",
        selected && "bg-muted",
      )}
    >
      <span className="flex w-full items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-sm">{catalog.display_name}</span>
        {showVersion && catalog.version && (
          <Badge variant="muted" className="font-mono text-[10px]">
            v{catalog.version}
          </Badge>
        )}
      </span>
      <span className="w-full text-xs text-muted-foreground">
        {catalog.spec_count} spec{catalog.spec_count === 1 ? "" : "s"}
        {catalog.ref_count > 0 ? (
          <span> · {catalog.ref_count} connection{catalog.ref_count === 1 ? "" : "s"}</span>
        ) : null}
      </span>
    </Button>
  );
}

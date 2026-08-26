import { FileText, Globe, Image } from "lucide-react";
import { useAssetsUsingTarget, type RefTarget } from "@/api/portal/hooks/assetRefs";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";

// UsedByAssets lists the assets whose content references this thing (#1475,
// #1488), so its owner sees what an edit or a delete would affect before doing
// it.
//
// One component serves both kinds because it answers one question, and the
// answer matters for the same reason either way: a reference hands the target
// the referencing asset's audience, so an asset with a public link makes the
// target readable by anyone holding that link. That is the flag, and it is why
// the section is worth a reader's attention rather than only their curiosity.
export function UsedByAssets({
  target,
  assetPath,
  onNavigate,
  className,
}: {
  target: RefTarget;
  /** Where a referencing asset opens for this reader. Absent, a row names the
   * asset without linking to it. */
  assetPath?: (assetId: string) => string;
  onNavigate?: (path: string) => void;
  /** Wrapper classes for the surface this sits on -- the asset sidebar rules a
   * line above each of its panels. It is on the section rather than around it,
   * so an asset nothing references leaves no stray divider behind. */
  className?: string;
}) {
  const { data, isError } = useAssetsUsingTarget(target.kind, target.id);
  const assets = data?.data ?? [];
  const hidden = data?.hidden ?? 0;
  const truncated = data?.truncated ?? false;

  if (isError || (assets.length === 0 && hidden === 0)) {
    return null;
  }

  const referenced = assets.length + hidden;
  return (
    <div className={className}>
      <SectionCard
        data-testid="used-by-assets"
        title={<SectionTitle kind={target.kind} count={referenced} truncated={truncated} />}
      >
        <ul className="space-y-1">
          {assets.map((a) => (
            <li key={a.id} className="flex items-center gap-2 text-xs text-muted-foreground">
              {assetPath && onNavigate ? (
                <button
                  type="button"
                  onClick={() => onNavigate(assetPath(a.id))}
                  className="min-w-0 truncate text-left text-primary hover:underline"
                >
                  {a.name}
                </button>
              ) : (
                <span className="truncate">{a.name}</span>
              )}
              {a.public && <PublicChip />}
            </li>
          ))}
        </ul>
        <HiddenNote count={hidden} kind={target.kind} />
        <p className="mt-2 text-xs text-muted-foreground">
          Deleting this {noun(target.kind)} leaves those assets rendering without it.
        </p>
      </SectionCard>
    </div>
  );
}

// noun names what is being held up, so every sentence in the section reads
// about the thing the reader is standing on.
function noun(kind: RefTarget["kind"]): string {
  return kind === "asset" ? "asset" : "resource";
}

// SectionTitle counts what the target is holding up. A bounded answer reads as
// a floor rather than a total: "used by 50 assets" on a list the server cut
// would understate exactly the thing this section exists to state.
function SectionTitle({
  kind,
  count,
  truncated,
}: {
  kind: RefTarget["kind"];
  count: number;
  truncated: boolean;
}) {
  const Icon = kind === "asset" ? FileText : Image;
  return (
    <span className="flex items-center gap-1.5">
      <Icon className="h-3 w-3 text-muted-foreground" />
      Used by {truncated ? "at least " : ""}
      {count} {count === 1 ? "asset" : "assets"}
    </span>
  );
}

// PublicChip marks an asset carrying an active link share. It is the fact the
// section exists for: the reference hands this target that asset's audience, so
// it is readable by anyone holding the link.
function PublicChip() {
  return (
    <Badge variant="warning" className="rounded px-1.5" data-testid="used-by-asset-public">
      <Globe className="h-3 w-3" />
      public link
    </Badge>
  );
}

// HiddenNote accounts for the referencing assets this reader may not open. They
// are counted rather than named, because a delete would break them too.
function HiddenNote({ count, kind }: { count: number; kind: RefTarget["kind"] }) {
  if (count === 0) return null;
  return (
    <p className="mt-2 text-xs text-muted-foreground" data-testid="used-by-assets-hidden">
      {count} more {count === 1 ? "asset references" : "assets reference"} this {noun(kind)} that
      you cannot open.
    </p>
  );
}

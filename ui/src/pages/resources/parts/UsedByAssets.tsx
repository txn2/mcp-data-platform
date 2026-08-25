import { Globe, Image } from "lucide-react";
import { useAssetsUsingResource } from "@/api/portal/hooks/assetResources";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";

// UsedByAssets lists the assets whose content references this file (#1475), so
// its owner sees what an edit or a delete would affect before doing it.
//
// It sits beside the prompts list because it answers the same question of a
// different consumer, and it carries one thing that list does not: a reference
// hands the file the asset's audience, so an asset with a public link makes the
// file readable by anyone holding that link. That is the flag, and it is the
// reason the section is worth a reader's attention rather than only their
// curiosity.
export function UsedByAssets({ resourceId }: { resourceId: string }) {
  const { data, isError } = useAssetsUsingResource(resourceId);
  const assets = data?.data ?? [];
  const hidden = data?.hidden ?? 0;
  const truncated = data?.truncated ?? false;

  if (isError || (assets.length === 0 && hidden === 0)) {
    return null;
  }

  const referenced = assets.length + hidden;
  return (
    <SectionCard
      data-testid="resource-used-by-assets"
      title={<SectionTitle count={referenced} truncated={truncated} />}
    >
      <ul className="space-y-1">
        {assets.map((a) => (
          <li key={a.id} className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="truncate">{a.name}</span>
            {a.public && <PublicChip />}
          </li>
        ))}
      </ul>
      <HiddenNote count={hidden} />
      <p className="mt-2 text-xs text-muted-foreground">
        Deleting this resource leaves those assets rendering without it.
      </p>
    </SectionCard>
  );
}

// SectionTitle counts what the file is holding up. A bounded answer reads as a
// floor rather than a total: "used by 50 assets" on a list the server cut would
// understate exactly the thing this section exists to state.
function SectionTitle({ count, truncated }: { count: number; truncated: boolean }) {
  return (
    <span className="flex items-center gap-1.5">
      <Image className="h-3 w-3 text-muted-foreground" />
      Used by {truncated ? "at least " : ""}
      {count} {count === 1 ? "asset" : "assets"}
    </span>
  );
}

// PublicChip marks an asset carrying an active link share. It is the fact the
// section exists for: the reference hands this file that asset's audience, so
// the file is readable by anyone holding the link.
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
function HiddenNote({ count }: { count: number }) {
  if (count === 0) return null;
  return (
    <p className="mt-2 text-xs text-muted-foreground" data-testid="used-by-assets-hidden">
      {count} more {count === 1 ? "asset references" : "assets reference"} this file that you
      cannot open.
    </p>
  );
}

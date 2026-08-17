import { Badge } from "@/components/ui/badge";
import { formatEntityUrn } from "@/lib/formatEntityUrn";

// UrnBadge is how a linked catalog entity is named in a dense list: the
// readable tail of its URN on a muted badge, with the whole URN on hover. It is
// deliberately not an EntityChip — nothing here is navigable, so nothing here is
// styled as though it were.
export function UrnBadge({ urn }: { urn: string }) {
  return (
    <Badge variant="muted" className="rounded font-mono" title={urn}>
      {formatEntityUrn(urn)}
    </Badge>
  );
}

/**
 * uniqueUrns is the list a caller should render: the entities a record names,
 * each once, in the order it named them.
 *
 * The write path normalizes what it stores (memory.NormalizeEntityURNs), but a
 * row written before that did not, and a record is about an entity once
 * whatever its row says. Every list here keys on the URN, so a repeat would be
 * dropped by React and logged as a duplicate key rather than rendered.
 */
export function uniqueUrns(urns: string[] | undefined): string[] {
  return [...new Set((urns ?? []).filter((urn) => urn.trim() !== ""))];
}

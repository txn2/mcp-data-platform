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

import { Badge } from "@/components/ui/badge";

type StatusVariant = "success" | "warning" | "danger" | "info" | "muted";

// The knowledge lifecycle statuses in one place, so a word means the same
// colour wherever it is read: the reviewer's queue, a reader's own insights and
// memories, a changeset row, and a page's lineage. Approved is `info` rather
// than `success` deliberately — an approved insight is cleared to be applied,
// not yet applied, and only `applied` is the finished state.
const STATUS: Record<string, { label: string; variant: StatusVariant }> = {
  pending: { label: "Pending", variant: "warning" },
  approved: { label: "Approved", variant: "info" },
  applied: { label: "Applied", variant: "success" },
  rejected: { label: "Rejected", variant: "danger" },
  rolled_back: { label: "Rolled Back", variant: "danger" },
  superseded: { label: "Superseded", variant: "muted" },
  active: { label: "Active", variant: "success" },
  stale: { label: "Stale", variant: "warning" },
  archived: { label: "Archived", variant: "muted" },
};

// knowledgeStatusVariant is the tint one status carries, for the rare caller
// that needs the variant without the badge (a row tint, a nested pill).
export function knowledgeStatusVariant(status: string): StatusVariant {
  return STATUS[status]?.variant ?? "muted";
}

// KnowledgeStatusBadge renders a stored lifecycle status as its display name on
// the matching badge tint. A status the map does not know renders as itself, so
// a value added server-side shows up rather than disappearing.
export function KnowledgeStatusBadge({ status }: { status: string }) {
  const known = STATUS[status];
  return (
    <Badge variant={known?.variant ?? "muted"}>{known?.label ?? status}</Badge>
  );
}

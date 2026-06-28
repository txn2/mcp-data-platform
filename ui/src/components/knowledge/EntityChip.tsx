import { FileText, MessageSquareText, FolderOpen, BookOpen, Plug, Database, Link2, Unlink } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { parseRef, entityHref, type ResolvedRef, type RefType } from "@/lib/entityRefs";

const TYPE_ICONS: Record<RefType, LucideIcon> = {
  asset: FileText,
  prompt: MessageSquareText,
  collection: FolderOpen,
  knowledge_page: BookOpen,
  connection: Plug,
  datahub: Database,
  unknown: Link2,
};

/**
 * EntityChip renders an entity reference (mcp:/urn:li:) as a typed chip: a type
 * icon plus the entity's display name. When the server has resolved the reference
 * it shows the real name; before resolution (or without a resolver) it falls back
 * to the name derived from the URN.
 *
 * It has three visual states so a chip never lies about being clickable (#709):
 * - A reference to a deleted entity (resolved.exists === false) renders as a
 *   broken-reference chip (struck through, broken-link icon) and is never a link.
 * - A live reference with an in-app route and an onNavigate handler deep-links to
 *   the target, in link (primary) styling.
 * - A live reference with no destination (e.g. a DataHub or connection ref, which
 *   has no in-portal view) renders as a neutral, non-link chip, so it is not
 *   styled to look clickable when it is not.
 */
export function EntityChip({
  urn,
  resolved,
  onNavigate,
}: {
  urn: string;
  resolved?: ResolvedRef;
  onNavigate?: (path: string) => void;
}) {
  const parsed = parseRef(urn);
  const type = (resolved?.type ?? parsed?.type ?? "unknown") as RefType;
  const label = resolved?.label ?? parsed?.fallbackLabel ?? urn;
  const broken = resolved ? !resolved.exists : false;
  const Icon = broken ? Unlink : (TYPE_ICONS[type] ?? Link2);

  const base =
    "not-prose mx-0.5 inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 align-baseline text-xs font-medium no-underline";

  const inner = (
    <>
      <Icon className="h-3 w-3 shrink-0" aria-hidden />
      <span>{label}</span>
    </>
  );

  // A live (non-broken) reference deep-links when it has a route and a navigator.
  const href = broken ? null : parsed && onNavigate ? entityHref(parsed.type, parsed.id) : null;
  if (href && onNavigate) {
    return (
      <a
        href={href}
        title={urn}
        onClick={(e) => {
          e.preventDefault();
          onNavigate(href);
        }}
        className={`${base} border-primary/20 bg-primary/10 text-primary cursor-pointer hover:bg-primary/20`}
      >
        {inner}
      </a>
    );
  }

  // Non-link chips: broken refs are struck through and muted; a live ref with no
  // destination is neutral (normal text) but never in link styling, so it does
  // not look clickable when it is not.
  const tone = broken
    ? "border-border bg-muted text-muted-foreground line-through"
    : "border-border bg-muted text-foreground";
  return (
    <span title={broken ? `${urn} (no longer exists)` : urn} className={`${base} ${tone}`}>
      {inner}
    </span>
  );
}

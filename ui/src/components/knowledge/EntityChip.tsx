import { FileText, MessageSquareText, FolderOpen, BookOpen, Plug, Database, Link2, Unlink } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { parseRef, refHref, type ResolvedRef, type RefType } from "@/lib/entityRefs";
import { Badge } from "@/components/ui/badge";

const TYPE_ICONS: Record<RefType, LucideIcon> = {
  asset: FileText,
  prompt: MessageSquareText,
  collection: FolderOpen,
  knowledge_page: BookOpen,
  connection: Plug,
  datahub: Database,
  unknown: Link2,
};

// A chip is a ui/badge sized for inline prose: square-cornered so it reads as a
// citation rather than a status pill, and out of the markdown renderer's typography.
const CHIP = "not-prose mx-0.5 rounded-md border align-baseline no-underline";

type ParsedRef = ReturnType<typeof parseRef>;

// chipIdentity is what a reference calls itself: the server's resolution when it
// has one, the URN's own shape when it does not, and the raw URN as a last
// resort — a chip always says something.
function chipIdentity(urn: string, parsed: ParsedRef, resolved?: ResolvedRef) {
  return {
    type: (resolved?.type ?? parsed?.type ?? "unknown") as RefType,
    label: resolved?.label ?? parsed?.fallbackLabel ?? urn,
  };
}

// chipFace adds where the reference goes, if anywhere: a destination only for a
// live reference with both a route and a navigator.
function chipFace(urn: string, hasNavigator: boolean, resolved?: ResolvedRef) {
  const parsed = parseRef(urn);
  const broken = resolved != null && !resolved.exists;
  const href =
    broken || !parsed || !hasNavigator
      ? null
      : refHref(parsed.type, parsed.id, parsed.urn);
  return { ...chipIdentity(urn, parsed, resolved), broken, href };
}

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
  const { type, label, broken, href } = chipFace(urn, onNavigate != null, resolved);
  const Icon = broken ? Unlink : (TYPE_ICONS[type] ?? Link2);

  const inner = (
    <>
      <Icon className="size-3 shrink-0" aria-hidden />
      <span>{label}</span>
    </>
  );

  // A live (non-broken) reference deep-links when it has a route and a navigator.
  // A catalog citation is navigable too: it opens the entity in the Catalog
  // tab, so a DataHub reference is a link everywhere a reference is rendered.
  if (href && onNavigate) {
    return (
      <Badge
        asChild
        variant="outline"
        className={`${CHIP} border-primary/20 bg-primary/10 text-primary [a&]:hover:bg-primary/20 [a&]:hover:text-primary`}
      >
        <a
          href={href}
          title={urn}
          onClick={(e) => {
            e.preventDefault();
            onNavigate(href);
          }}
        >
          {inner}
        </a>
      </Badge>
    );
  }

  // Non-link chips: broken refs are struck through and muted; a live ref with no
  // destination is neutral (normal text) but never in link styling, so it does
  // not look clickable when it is not.
  return (
    <Badge
      variant="muted"
      title={broken ? `${urn} (no longer exists)` : urn}
      className={`${CHIP} border-border ${broken ? "line-through" : "text-foreground"}`}
    >
      {inner}
    </Badge>
  );
}

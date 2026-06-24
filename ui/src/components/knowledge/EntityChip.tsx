import { FileText, MessageSquareText, FolderOpen, BookOpen, Plug, Database, Link2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { parseRef, type ResolvedRef, type RefType } from "@/lib/entityRefs";

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
 * EntityChip renders an inline reference (mcp:/urn:li:) from a knowledge-page body
 * as a typed chip: a type icon plus the entity's display name. When the server has
 * resolved the reference, the real name is shown and a missing target is greyed out
 * and struck through; before resolution (or app-wide, without a resolver) it falls
 * back to the name derived from the URN itself.
 */
export function EntityChip({ urn, resolved }: { urn: string; resolved?: ResolvedRef }) {
  const parsed = parseRef(urn);
  const type = (resolved?.type ?? parsed?.type ?? "unknown") as RefType;
  const Icon = TYPE_ICONS[type] ?? Link2;
  const label = resolved?.label ?? parsed?.fallbackLabel ?? urn;
  const missing = resolved ? !resolved.exists : false;

  return (
    <span
      title={urn}
      className={`not-prose mx-0.5 inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 align-baseline text-xs font-medium no-underline ${
        missing
          ? "border-border bg-muted text-muted-foreground line-through"
          : "border-primary/20 bg-primary/10 text-primary"
      }`}
    >
      <Icon className="h-3 w-3 shrink-0" aria-hidden />
      <span>{label}</span>
    </span>
  );
}

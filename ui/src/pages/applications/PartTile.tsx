import { ChartColumnBig, Database, Globe, Rss, Zap } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { ApplicationPartKind } from "@/api/portal/hooks/applications";
import { cn } from "@/lib/utils";

/** The tile each kind's icon sits in, the same on the card and in the dialog. */
const PART_TILE: Record<ApplicationPartKind, { icon: LucideIcon; className: string }> = {
  script: { icon: Zap, className: "bg-blue-500/10 text-blue-600 dark:text-blue-400" },
  asset: { icon: ChartColumnBig, className: "bg-teal-500/10 text-teal-600 dark:text-teal-300" },
  source: { icon: Rss, className: "bg-violet-500/10 text-violet-600 dark:text-violet-300" },
  schema: { icon: Database, className: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-300" },
  site: { icon: Globe, className: "bg-sky-500/10 text-sky-600 dark:text-sky-300" },
};

export function PartTile({ kind, muted = false, size = 36 }: { kind: ApplicationPartKind; muted?: boolean; size?: number }) {
  const tile = PART_TILE[kind];
  const Icon = tile.icon;
  return (
    <span
      aria-hidden="true"
      style={{ width: size, height: size }}
      className={cn(
        "flex shrink-0 items-center justify-center rounded-lg",
        muted ? "bg-muted text-muted-foreground" : tile.className,
      )}
    >
      <Icon className="size-[18px]" />
    </span>
  );
}


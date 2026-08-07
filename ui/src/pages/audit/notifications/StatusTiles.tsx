import type { NotificationStats, NotificationStatus } from "@/api/admin/hooks";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// STAT_TILES is the at-a-glance health read, ordered by what an admin looks
// for first: what broke, then what is still in flight, then what worked.
export const STAT_TILES: { key: NotificationStatus; label: string; help: string }[] = [
  {
    key: "failed",
    label: "Failed",
    help: "Delivery attempts exhausted; these emails were never sent",
  },
  { key: "pending", label: "Pending", help: "Queued and waiting for the send worker" },
  { key: "sending", label: "Sending", help: "Claimed by a worker and in flight" },
  { key: "sent", label: "Sent", help: "Handed to the mail server" },
];

// StatusTiles is the at-a-glance health read. Each tile doubles as a filter,
// since "7 failed" is a number an admin immediately wants the rows behind.
export function StatusTiles({
  stats,
  active,
  onPick,
}: {
  stats?: NotificationStats;
  active: string;
  onPick: (key: NotificationStatus) => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {STAT_TILES.map((tile) => (
        <Button
          key={tile.key}
          type="button"
          variant="outline"
          title={tile.help}
          aria-pressed={active === tile.key}
          onClick={() => onPick(tile.key)}
          className={cn(
            "h-auto flex-col items-start gap-1 bg-card p-3 text-left font-normal",
            active === tile.key && "border-primary/40 ring-1 ring-primary/30",
          )}
        >
          <span className="text-xs text-muted-foreground">{tile.label}</span>
          <span className="text-2xl font-semibold tabular-nums">
            {stats ? stats[tile.key] : "-"}
          </span>
        </Button>
      ))}
    </div>
  );
}

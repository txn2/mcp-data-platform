import { FileCode2 } from "lucide-react";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { ThumbCard } from "@/components/cards/ThumbCard";
import { Badge } from "@/components/ui/badge";
import { useResolvedDark } from "@/stores/theme";
import { InertBadge, LastRunCell, LibraryBadge, ScheduleCell } from "./ScriptRow";

// ScriptGrid is the scripts listing as a grid (#1909): one card per script,
// its flow diagram as the tile, then what a row says today. A listing of
// pipelines read this way tells a loader from a report from a dashboard
// refresh at a glance, the way a pipeline tool's canvas list does.

// SCRIPT_THUMBNAIL_BASE is where a script's tile is served from.
export const SCRIPT_THUMBNAIL_BASE = "/api/v1/portal/scripts";

// scriptTileSrc is a script's tile, addressed by the version it should show,
// so saving a version is a new address and a new tile without a stale cache.
export function scriptTileSrc(id: string, version: number, dark: boolean): string {
  const variant = dark ? "&variant=dark" : "";
  return `${SCRIPT_THUMBNAIL_BASE}/${id}/thumbnail?v=${version}${variant}`;
}

export function ScriptGrid({
  rows,
  basePath,
  onNavigate,
}: {
  rows: PortalScriptRow[];
  basePath: string;
  onNavigate: (path: string) => void;
}) {
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4" data-testid="script-grid">
      {rows.map((row) => (
        <ScriptCard key={row.script.id} row={row} basePath={basePath} onNavigate={onNavigate} />
      ))}
    </div>
  );
}

function ScriptCard({
  row,
  basePath,
  onNavigate,
}: {
  row: PortalScriptRow;
  basePath: string;
  onNavigate: (path: string) => void;
}) {
  const { script } = row;
  const dark = useResolvedDark();
  return (
    // A script with no tile yet, or whose latest version cannot be drawn, is
    // answered 404 and shows the icon a tile-less asset shows.
    <ThumbCard
      onClick={() => onNavigate(`${basePath}/${script.id}`)}
      thumbnailSrc={scriptTileSrc(script.id, script.version, dark)}
      fallbackIcon={FileCode2}
    >
      <div className="w-full space-y-2" data-testid={`script-card-${script.id}`}>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{script.display_name || script.name}</span>
          <LibraryBadge row={row} />
          {script.category && <Badge variant="muted">{script.category}</Badge>}
          <InertBadge row={row} />
        </div>
        <div className="font-mono text-xs text-muted-foreground">{script.name}</div>
        {/* The row's cells are inline in a table cell; in a card each is its
            own line. */}
        <div>
          <ScheduleCell row={row} />
        </div>
        <div>
          <LastRunCell row={row} />
        </div>
      </div>
    </ThumbCard>
  );
}

import { useEffect, useState } from "react";
import { BookOpen } from "lucide-react";
import { THUMB_HEIGHT, THUMB_WIDTH } from "@/lib/thumbnailSupport";

/**
 * LIBRARY_TILE_TYPE is the content type a library's tile reaches the tile page
 * as (#1970), the same constant the tile worker sends
 * (internal/platform/thumbworker.LibraryTileType). It is no file's type.
 */
export const LIBRARY_TILE_TYPE = "application/vnd.mcp-data-platform.library";

/**
 * LibraryTile is a library's tile: a book, its name and the version it shows.
 * A library makes no platform calls, so it has no diagram; the flow tile would
 * draw it as a script that does nothing.
 */
export function LibraryTile({
  name,
  content,
  onDrawn,
}: {
  name: string;
  content: string;
  onDrawn: (reason: string) => void;
}) {
  const [version, setVersion] = useState<number | null>(null);

  useEffect(() => {
    try {
      const v = (JSON.parse(content) as { version?: unknown }).version;
      if (typeof v !== "number") throw new Error("no version");
      setVersion(v);
    } catch {
      onDrawn("the library tile could not be read");
    }
  }, [content, onDrawn]);

  useEffect(() => {
    if (version === null) return;
    requestAnimationFrame(() => requestAnimationFrame(() => onDrawn("")));
  }, [version, onDrawn]);

  if (version === null) return null;
  return (
    <div
      data-testid="library-tile"
      style={{
        width: THUMB_WIDTH,
        height: THUMB_HEIGHT,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 12,
        padding: 24,
        boxSizing: "border-box",
        background: "hsl(var(--background))",
        color: "hsl(var(--foreground))",
        font: "14px ui-sans-serif, system-ui, sans-serif",
      }}
    >
      <BookOpen size={72} strokeWidth={1.5} style={{ color: "hsl(var(--primary))" }} aria-hidden />
      <div
        style={{
          font: "600 20px ui-monospace, SFMono-Regular, Menlo, monospace",
          maxWidth: "100%",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {name}
      </div>
      <div style={{ color: "hsl(var(--muted-foreground))" }}>Version {version}</div>
    </div>
  );
}

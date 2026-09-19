import { createRoot } from "react-dom/client";
import { Tile, type TileData } from "./components/thumbnail/Tile";

declare global {
  interface Window {
    /**
     * Settles with "" once the tile is drawn, or with the reason it cannot be.
     * The platform's renderer waits on it before taking the screenshot.
     */
    __tileReady?: Promise<string>;
  }
}

// The page a tile is drawn from (#1787). The platform writes the document into
// #tile-data and opens this page in its headless renderer; nothing here is
// reached by a reader.
let settle: (reason: string) => void = () => {};
window.__tileReady = new Promise<string>((resolve) => {
  settle = resolve;
});

try {
  const data = JSON.parse(document.getElementById("tile-data")?.textContent ?? "") as TileData;
  const dark = document.documentElement.classList.contains("dark");
  const root = document.getElementById("tile-root");
  if (!root) throw new Error("the tile page has no #tile-root");
  createRoot(root).render(<Tile data={data} dark={dark} onDrawn={settle} />);
} catch (err) {
  settle(`the tile page could not be read: ${String(err)}`);
}

/**
 * Recovering a page whose code chunk did not load (#2139).
 *
 * Chunk names carry a content hash, and during a rolling update a page from
 * one build can ask a replica on the other build for a chunk it does not
 * have. The server answers 404; reloading fetches the current index.html,
 * which names the chunks that exist. The portal does that once on its own,
 * and shows its error page if the reloaded page fails the same way.
 */

/** How long after one automatic reload a second is withheld. */
export const CHUNK_RELOAD_WINDOW_MS = 30_000;

const STORAGE_KEY = "mcp-portal-chunk-reload-at";

// The messages a rejected dynamic import carries in Chromium, Firefox and
// Safari, and the one Vite rejects a lazy module's stylesheet preload with.
const CHUNK_ERROR = /dynamically imported module|Importing a module script failed|Unable to preload CSS/i;

/** Whether an error is a code chunk that did not load. */
export function isChunkLoadError(err: unknown): boolean {
  return err instanceof Error && CHUNK_ERROR.test(err.message);
}

function lastReloadAt(): number {
  try {
    return Number(sessionStorage.getItem(STORAGE_KEY)) || 0;
  } catch {
    return 0;
  }
}

function recordReload(now: number) {
  try {
    sessionStorage.setItem(STORAGE_KEY, String(now));
  } catch {
    // Without storage the reload still happens; a second failure inside the
    // window then reloads again, which a browser with storage avoids.
  }
}

/**
 * Reloads the page when `err` is a chunk that did not load and no automatic
 * reload happened in the last CHUNK_RELOAD_WINDOW_MS. Returns whether it did.
 */
export function reloadOnceForChunkError(
  err: unknown,
  now: number = Date.now(),
  reload: () => void = () => window.location.reload(),
): boolean {
  if (!isChunkLoadError(err)) return false;
  const last = lastReloadAt();
  if (last && now - last < CHUNK_RELOAD_WINDOW_MS) return false;
  recordReload(now);
  reload();
  return true;
}

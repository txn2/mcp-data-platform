import type { ThumbnailTarget } from "@/lib/thumbnailSupport";

/**
 * Telling the background queue to forget what it has tried for one target.
 *
 * The queue gives a target three attempts per reason and then leaves it alone
 * for the life of the tab. That bound is right -- a document nothing can
 * rasterize should not be retried forever -- but it made the one control a
 * reader has over a missing tile inert: Recapture moved no row state, so the
 * attempt key did not change, so the asset the queue had given up on was never
 * offered again (#1753).
 *
 * A press says "try this one again", which is exactly an instruction to the
 * queue and is not expressible as row state. It is a module-level event rather
 * than context because the queue is mounted once in the shell and the panel is
 * several pages away from it, and because any surface that gains a recapture
 * control later should reach it the same way.
 */

type Listener = (target: ThumbnailTarget) => void;

const listeners = new Set<Listener>();

/** Forget every attempt recorded against this target. */
export function resetCaptureAttempts(target: ThumbnailTarget): void {
  for (const listener of [...listeners]) listener(target);
}

/** Subscribe to resets; the returned function unsubscribes. */
export function onCaptureAttemptsReset(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

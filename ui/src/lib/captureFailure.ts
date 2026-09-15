import type { ThumbnailTarget } from "@/lib/thumbnailSupport";

/**
 * Why a thumbnail capture did not produce an image.
 *
 * Every path a capture could fail on used to discard its reason -- three bare
 * `catch` clauses and a `.catch(() => ...)` -- so an asset with no tile was
 * indistinguishable from an asset whose tile was being taken, forever, and the
 * exception naming the cause was thrown away on every one of the queue's
 * attempts (#1752). #1751 went undiagnosed for exactly that reason.
 *
 * It lives beside lib/thumbnailSupport rather than in lib/thumbnail because the
 * panel that shows a failure must be able to name one without pulling in
 * html2canvas.
 */

/** What went wrong, at the granularity a reader can act on. */
export type CaptureFailureCode =
  /** A file the document links to could not be loaded, so the tile would be a picture of the error. */
  | "references"
  /** The document could not be rasterized. */
  | "render"
  /** The image was drawn but the server would not take it. */
  | "upload"
  /** Nothing finished inside the capture window. */
  | "timeout"
  /** The document's own bytes could not be read. */
  | "content"
  /** Nothing in this browser draws this content type. */
  | "unsupported";

export interface CaptureFailure {
  code: CaptureFailureCode;
  /** The exception or status, as text, for the console and the panel. */
  detail: string;
  /**
   * Whether trying the same capture again could produce a different answer.
   *
   * A document that throws in the rasterizer throws every time, and one whose
   * linked file 404s keeps 404ing: presenting either as "being taken" is a
   * promise nothing will keep (#1752). A timeout, a failed upload and an
   * unreadable body are the transient ones.
   */
  transient: boolean;
}

/** The codes that say waiting, or pressing again, could produce an image. */
const TRANSIENT: ReadonlySet<CaptureFailureCode> = new Set<CaptureFailureCode>([
  "upload",
  "timeout",
  "content",
]);

/** One failure, with its detail reduced to a line of text. */
export function captureFailure(code: CaptureFailureCode, detail: unknown): CaptureFailure {
  return { code, detail: describe(detail), transient: TRANSIENT.has(code) };
}

/** An exception, a response, or anything else a catch clause was handed, as one line. */
function describe(detail: unknown): string {
  if (detail instanceof Error) {
    return detail.name ? `${detail.name}: ${detail.message}` : detail.message;
  }
  if (typeof detail === "string") return detail;
  try {
    return JSON.stringify(detail) ?? String(detail);
  } catch {
    return String(detail);
  }
}

/**
 * Write a failure to the browser console and hand it to the caller, in that
 * order.
 *
 * One function rather than a console line at each catch clause: the line is the
 * only record a capture leaves anywhere -- it happens in the reader's browser
 * and writes nothing to a server -- so a path that forgot it is a path whose
 * failures are invisible again (#1752). The kind and the id are in it because
 * "a capture failed" cannot be matched to the file whose tile is missing.
 */
export function failCapture(
  target: ThumbnailTarget,
  onFailed: ((failure: CaptureFailure) => void) | undefined,
  code: CaptureFailureCode,
  detail: unknown,
): CaptureFailure {
  const failure = captureFailure(code, detail);
  console.error(
    `thumbnail: capture of ${target.kind} ${target.id} failed (${failure.code}): ${failure.detail}`,
  );
  onFailed?.(failure);
  return failure;
}

/**
 * What the panel says about a failure, written for the person looking at it.
 *
 * The distinction that matters to them is whether there is any point waiting,
 * which is what `transient` says; the detail follows so an owner can report it
 * or recognize their own document in it.
 */
export function failureSentence(failure: CaptureFailure): string {
  return `${REASONS[failure.code]} ${failure.transient ? "Trying again may work." : "Trying again will give the same result unless the file changes."}`;
}

const REASONS: Record<CaptureFailureCode, string> = {
  references: "The preview picture could not be made: a file this document links to could not be loaded.",
  render: "The preview picture could not be made: this document could not be drawn.",
  upload: "The preview picture was made but could not be saved.",
  timeout: "The preview picture could not be made in time.",
  content: "The preview picture could not be made: this file's contents could not be read.",
  unsupported: "Nothing in this browser can draw this kind of file.",
};

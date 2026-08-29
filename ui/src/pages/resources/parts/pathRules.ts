/**
 * The folder-path rules, as the browser states them (#1529).
 *
 * The server validates every path again and is the authority; this exists so a
 * person typing a destination is told which rule they broke while they are
 * typing it, rather than after a round trip. The rules and the wording are the
 * ones pkg/resource/path.go applies, so the two answers agree.
 */

export const MAX_PATH_SEGMENTS = 8;
export const MAX_PATH_LENGTH = 200;

const SEGMENT_RE = /^[a-z][a-z0-9-]{0,30}$/;

/**
 * pathProblem states why a folder path cannot be used, or null when it can.
 *
 * Each message names the rule that was broken rather than restating the whole
 * grammar: the person typed one path and needs to know which part of it is the
 * problem.
 */
export function pathProblem(path: string): string | null {
  if (path === "") return "A folder path is required.";
  if (path.startsWith("/") || path.endsWith("/")) {
    return "A path must not start or end with a slash.";
  }
  const parts = path.split("/");
  // Depth before length: a path that breaks both is fixed by removing folders,
  // and naming the character limit would send the person shortening names that
  // were never the problem.
  if (parts.length > MAX_PATH_SEGMENTS) {
    return `A path is at most ${MAX_PATH_SEGMENTS} folders deep; this one is ${parts.length}.`;
  }
  if (path.length > MAX_PATH_LENGTH) {
    return `A path is at most ${MAX_PATH_LENGTH} characters; this one is ${path.length}.`;
  }
  return parts.map(segmentProblem).find((problem) => problem !== null) ?? null;
}

/** segmentProblem states why one folder name cannot be used, or null. */
function segmentProblem(part: string): string | null {
  if (part === "") return "A path has an empty folder name in it.";
  if (part === "." || part === "..") return `"${part}" names no folder.`;
  if (!SEGMENT_RE.test(part)) {
    return `"${part}" must be lowercase letters, digits and hyphens, starting with a letter, at most 31 characters.`;
  }
  return null;
}

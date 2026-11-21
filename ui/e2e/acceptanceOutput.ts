import path from "path";
import { fileURLToPath } from "url";

const repo = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);

/**
 * Where one run of a suite an acceptance criterion drives writes its
 * screenshots, error context and traces: build/<suite>-results/<run>/, which
 * the acceptance job uploads (#2694).
 *
 * Playwright empties its output directory before every run, and several
 * criteria run the same suite one after another, so a directory shared by the
 * suite kept only the last run's output: #2748's failing criterion had its
 * screenshots removed by the passing criterion after it. Each run writes a
 * directory of its own.
 */
export function acceptanceOutput(suite: string): string {
  const run = `${new Date().toISOString().replace(/[:.]/g, "-")}-${process.pid}`;
  return path.join(repo, "build", `${suite}-results`, run);
}

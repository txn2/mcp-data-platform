/**
 * Where each file of a bulk upload stands, and what the batch comes to (#1862).
 */

import type { UploadOutcome } from "./send";

/** One file's state in the batch. */
export type ItemStatus =
  | { state: "ready" }
  | { state: "refused"; error: string }
  | { state: "sending"; progress: number }
  | { state: "done"; outcome: UploadOutcome }
  | { state: "failed"; error: string };

/** The batch in numbers, which is what the summary line states. */
export interface BatchSummary {
  created: number;
  revised: number;
  unchanged: number;
  failed: number;
  refused: number;
  pending: number;
}

/** summarize counts the batch by state and outcome. */
export function summarize(statuses: Iterable<ItemStatus>): BatchSummary {
  const s: BatchSummary = { created: 0, revised: 0, unchanged: 0, failed: 0, refused: 0, pending: 0 };
  for (const status of statuses) {
    if (status.state === "done") s[status.outcome] += 1;
    else if (status.state === "failed") s.failed += 1;
    else if (status.state === "refused") s.refused += 1;
    else s.pending += 1;
  }
  return s;
}

/** summaryText is the sentence the dialog shows once a batch has run. */
export function summaryText(s: BatchSummary): string {
  const parts = [
    `${s.created} created`,
    `${s.revised} new ${s.revised === 1 ? "version" : "versions"}`,
    `${s.unchanged} unchanged`,
    `${s.failed} failed`,
  ];
  if (s.refused > 0) parts.push(`${s.refused} not sent`);
  return parts.join(", ");
}

/** What each outcome is called in the list. */
export const OUTCOME_LABELS: Record<UploadOutcome, string> = {
  created: "Created",
  revised: "New version",
  unchanged: "Unchanged",
};

import { StatusBadge } from "@/components/cards/StatusBadge";
import type { CallOutcome, CallRecord, CallSatisfiedBy } from "@/api/admin/types";

// A record's outcome is the one thing a reader scans this catalog for, so it
// is a badge rather than a word in a cell: satisfied is the only outcome that
// says the call answered something, and it should be findable at a glance down
// a column of drafts.

const OUTCOME_VARIANT: Record<CallOutcome, "success" | "error" | "warning" | "neutral"> = {
  satisfied: "success",
  failed: "error",
  superseded: "warning",
  ran: "neutral",
};

/** What each outcome means, shown on hover rather than in a legend nobody reads. */
export const OUTCOME_DESCRIPTION: Record<CallOutcome, string> = {
  satisfied:
    "Something was built from this call: an asset, an export, or a capture that named it.",
  failed: "The call returned an error.",
  superseded:
    "A later call in the same session ran over the same tables, and nothing was built from this one.",
  ran: "The call succeeded and nothing has come of it yet.",
};

/** How a satisfied record came to be satisfied. */
export const SATISFIED_BY_LABEL: Record<CallSatisfiedBy, string> = {
  asset: "saved as an asset",
  export: "exported",
  capture: "captured by the agent",
};

export function OutcomeBadge({ outcome }: { outcome: CallOutcome }) {
  return (
    <span title={OUTCOME_DESCRIPTION[outcome]}>
      <StatusBadge variant={OUTCOME_VARIANT[outcome]}>{outcome}</StatusBadge>
    </span>
  );
}

/** The one-line summary of what a call did, for a list cell. */
export function callSummary(record: CallRecord): string {
  if (record.kind === "api") {
    return [record.method, record.path].filter(Boolean).join(" ") || record.operation_id || record.tool_name;
  }
  return record.statement || record.tool_name;
}

/** What a call is called: the purpose stated for it, or what it addressed. */
export function callTitle(record: CallRecord): string {
  return record.purpose?.trim() || callSummary(record);
}

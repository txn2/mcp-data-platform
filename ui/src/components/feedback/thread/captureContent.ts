import type { ThreadEvent } from "@/api/portal/types";

// The bounds the server holds an insight's text to: MinContentLen and
// MaxContentLen in pkg/memory/types.go. The server still enforces them; these
// only say what the panel offers, and a refusal shows the server's own words.
export const INSIGHT_MIN_BYTES = 10;
export const INSIGHT_MAX_BYTES = 4000;

/** insightBytes is the size the server measures: UTF-8 bytes of the trimmed text. */
export function insightBytes(text: string): number {
  return new TextEncoder().encode(text.trim()).length;
}

/**
 * defaultInsightContent is the text the server builds when none is sent
 * (threadInsightContent in internal/portal/feedbackapi/insight.go): the title
 * and the thread's first message, or whichever of them it has.
 */
export function defaultInsightContent(title: string | undefined, events: ThreadEvent[] | undefined): string {
  const heading = title?.trim() ?? "";
  const first = (events ?? []).map((e) => e.body?.trim() ?? "").find((body) => body !== "") ?? "";
  return heading && first ? `${heading}\n\n${first}` : first || heading;
}

export interface InsightTextCheck {
  /** ok says the text can be submitted. */
  ok: boolean;
  /** bad says the hint is a problem to fix rather than a note. */
  bad: boolean;
  /** over says the text is past the limit, which the count shows too. */
  over: boolean;
  hint: string;
  count: string;
}

const formatted = (n: number) => n.toLocaleString("en-US");

/** checkInsightText says whether a reviewer's text can be captured, and why not. */
export function checkInsightText(text: string): InsightTextCheck {
  const bytes = insightBytes(text);
  const count = `${formatted(bytes)} of ${formatted(INSIGHT_MAX_BYTES)} bytes`;
  if (bytes > INSIGHT_MAX_BYTES) {
    const over = formatted(bytes - INSIGHT_MAX_BYTES);
    return { ok: false, bad: true, over: true, count, hint: `${over} bytes over the limit. Shorten the text to capture this.` };
  }
  if (bytes < INSIGHT_MIN_BYTES) {
    return { ok: false, bad: true, over: false, count, hint: `Write at least ${INSIGHT_MIN_BYTES} bytes.` };
  }
  return { ok: true, bad: false, over: false, count, hint: "The insight is created from this text." };
}

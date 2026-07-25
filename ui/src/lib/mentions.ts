// @-mention tokens in feedback comment bodies (#627).
//
// Token grammar: "@" local "(" domain ")", e.g. @marcus.johnson(example.com).
// It mirrors pkg/portal/mention exactly -- the server parses the same shape out
// of the stored body -- so a change here without a change there silently breaks
// delivery. The parenthesized domain terminates the token, which is what lets a
// mention be written immediately before sentence punctuation.
const MENTION_PATTERN = /@([A-Za-z0-9._%+-]+)\(([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,})\)/g;

// Characters that could belong to an email address written immediately before a
// token. When one precedes the "@", the match is that address's separator
// rather than a mention.
const ADDRESS_CHAR = /[A-Za-z0-9._%+@-]/;

export interface MentionMatch {
  /** The token as written, e.g. "@marcus.johnson(example.com)". */
  raw: string;
  /** The address it names, lower-cased. */
  email: string;
  /** Index of the token's "@" within the source text. */
  start: number;
  /** Index one past the token's closing parenthesis. */
  end: number;
}

/** scanMentions returns every mention token in text, in the order written. */
export function scanMentions(text: string): MentionMatch[] {
  const out: MentionMatch[] = [];
  for (const m of text.matchAll(MENTION_PATTERN)) {
    const start = m.index ?? 0;
    if (start > 0 && ADDRESS_CHAR.test(text[start - 1]!)) continue;
    out.push({
      raw: m[0],
      email: `${m[1]}@${m[2]}`.toLowerCase(),
      start,
      end: start + m[0].length,
    });
  }
  return out;
}

/**
 * formatMention renders an address as a mention token, or "" when the address
 * cannot be written as one (no "@", empty half, or characters outside the
 * grammar). The result is checked by scanning it back, so the composer can
 * never insert a token the server would read as something else.
 */
export function formatMention(email: string): string {
  const trimmed = email.trim();
  const at = trimmed.indexOf("@");
  if (at <= 0 || at === trimmed.length - 1) return "";
  const token = `@${trimmed.slice(0, at)}(${trimmed.slice(at + 1)})`;
  const scanned = scanMentions(token);
  if (scanned.length !== 1 || scanned[0]!.email !== trimmed.toLowerCase()) return "";
  return token;
}

/** A run of body text: either literal text or a resolved mention. */
export type BodySegment =
  | { kind: "text"; text: string }
  | { kind: "mention"; text: string; email: string };

/**
 * splitBody breaks a comment body into text and mention runs for rendering.
 * The mention text stays exactly as it was written; the caller decides how to
 * display it (a name chip when the address is known, the raw token otherwise).
 */
export function splitBody(body: string): BodySegment[] {
  const segments: BodySegment[] = [];
  let cursor = 0;
  for (const m of scanMentions(body)) {
    if (m.start > cursor) segments.push({ kind: "text", text: body.slice(cursor, m.start) });
    segments.push({ kind: "mention", text: m.raw, email: m.email });
    cursor = m.end;
  }
  if (cursor < body.length) segments.push({ kind: "text", text: body.slice(cursor) });
  return segments;
}

/** An in-progress "@..." the caret sits in, which drives the type-ahead. */
export interface MentionQuery {
  /** What the user has typed after the "@". */
  query: string;
  /** Index of the "@" that opened it. */
  start: number;
}

// A trigger runs from "@" to the caret and holds no whitespace or closing
// paren: once either appears the user has moved on and the picker closes.
const TRIGGER_STOP = /[\s()]/;

/**
 * activeMentionQuery returns the mention being typed at the caret, or null.
 * The "@" must start the text or follow whitespace or an opening bracket, so
 * typing an email address never opens the picker.
 */
export function activeMentionQuery(text: string, caret: number): MentionQuery | null {
  for (let i = caret - 1; i >= 0 && caret - i <= 64; i--) {
    const ch = text[i]!;
    if (ch === "@") {
      const before = i > 0 ? text[i - 1]! : "";
      if (before !== "" && !/[\s([{<]/.test(before)) return null;
      return { query: text.slice(i + 1, caret), start: i };
    }
    if (TRIGGER_STOP.test(ch)) return null;
  }
  return null;
}

/**
 * replaceMentionQuery swaps the in-progress "@..." for a finished token,
 * returning the new text and the caret position after it.
 */
export function replaceMentionQuery(
  text: string,
  active: MentionQuery,
  email: string,
): { text: string; caret: number } {
  const token = formatMention(email);
  if (!token) return { text, caret: active.start + 1 + active.query.length };
  const head = text.slice(0, active.start);
  const tail = text.slice(active.start + 1 + active.query.length);
  const withSpace = tail.startsWith(" ") ? "" : " ";
  return { text: `${head}${token}${withSpace}${tail}`, caret: (head + token + withSpace).length };
}

import type { WebhookSourceStatus } from "@/api/admin/types";
import { sortNames } from "@/lib/sortByName";

// The Webhooks page's search and filters (#2792): what a source is matched
// on, the kinds offered, and how the filter is kept in the page's address so
// a filtered list can be bookmarked or sent to a teammate and survives a
// reload. A deployment hosting forms for several sites holds one form-<name>
// source per endpoint, so finding one by scrolling stops working quickly.

export const SEARCH_PARAM = "q";
export const KIND_PARAM = "kind";
export const REJECTED_PARAM = "rejected";

export interface SourceFilter {
  /** Text every word of which a source's name, table or connection holds. */
  q: string;
  /** What feeds the source: http, imap, form, postgres; empty is any. */
  kind: string;
  /** Only the sources that refused a request in the last day. */
  rejected: boolean;
}

export const NO_FILTER: SourceFilter = { q: "", kind: "", rejected: false };

/** KIND_LABELS name each input as the page says it. */
export const KIND_LABELS: Record<string, string> = {
  http: "Inbound",
  form: "Form",
  imap: "Mailbox",
  postgres: "Database",
};

/** readFilter reads the filter from a query string. */
export function readFilter(search: string): SourceFilter {
  const params = new URLSearchParams(search);
  return {
    q: params.get(SEARCH_PARAM) ?? "",
    kind: params.get(KIND_PARAM) ?? "",
    rejected: params.get(REJECTED_PARAM) === "1",
  };
}

/** writeFilter sets the filter on params, removing a parameter left empty. */
export function writeFilter(params: URLSearchParams, f: SourceFilter): URLSearchParams {
  const out = new URLSearchParams(params);
  for (const [key, value] of [
    [SEARCH_PARAM, f.q.trim()],
    [KIND_PARAM, f.kind],
    [REJECTED_PARAM, f.rejected ? "1" : ""],
  ] as const) {
    if (value === "") out.delete(key);
    else out.set(key, value);
  }
  return out;
}

/** rejectedLastDay is how many requests the source refused in the last day:
 * every outcome other than accepted. */
export function rejectedLastDay(s: WebhookSourceStatus): number {
  return Object.entries(s.last_day ?? {}).reduce((n, [outcome, count]) => (outcome === "accepted" ? n : n + count), 0);
}

/** matches reports whether a source matches the search text: every word of
 * it is found, case-insensitively, in the name, the table, the connection or
 * the kind. */
export function matches(s: WebhookSourceStatus, q: string): boolean {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const haystack = [s.name, s.table, s.connection, s.input, KIND_LABELS[s.input] ?? ""].join("\n").toLowerCase();
  return words.every((w) => haystack.includes(w));
}

/** filterSources keeps the sources the filter admits, in the order given. */
export function filterSources(list: WebhookSourceStatus[], f: SourceFilter): WebhookSourceStatus[] {
  return list.filter(
    (s) => (f.kind === "" || s.input === f.kind) && (!f.rejected || rejectedLastDay(s) > 0) && matches(s, f.q),
  );
}

/** isFiltered reports whether the filter narrows anything. */
export function isFiltered(f: SourceFilter): boolean {
  return f.q.trim() !== "" || f.kind !== "" || f.rejected;
}

/** kindOptions are the kinds to filter by: the ones the deployment has, plus
 * one the address names that no source has, so the filter shows what it
 * holds. */
export function kindOptions(list: WebhookSourceStatus[], current = ""): { value: string; label: string }[] {
  const present = new Set<string>(list.map((s) => s.input));
  if (current !== "") present.add(current);
  const order = ["http", "form", "imap", "postgres"];
  const kinds = [...order.filter((k) => present.has(k)), ...sortNames([...present].filter((k) => !order.includes(k)))];
  return [{ value: "", label: "All kinds" }, ...kinds.map((k) => ({ value: k, label: KIND_LABELS[k] ?? k }))];
}

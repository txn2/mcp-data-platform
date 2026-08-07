import { ApiError } from "@/api/admin/client";
import type { ConnectionAuthEvent } from "@/api/admin/types";

// The OAuth status card's text helpers. Extracted from
// ConnectionOAuthStatusCard.tsx (#1206) so the wording rules stay testable
// without rendering, and so the card file holds only its layout.

// formatActionError turns any thrown value into a non-empty,
// operator-meaningful string. Rendering `err.message` directly produced an
// empty red box when an ApiError carried an empty detail (some upstream paths
// return 502 with no body, and HTTP/2 fetches return empty statusText). An
// empty error box is the worst of both worlds: the operator sees something
// went wrong but learns nothing about what. Always fall back to the caller's
// label, and append the HTTP status when available so "Refresh failed (HTTP
// 502)" beats a blank box.
export function formatActionError(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    const detail = err.detail?.trim();
    if (detail) return detail;
    return err.status > 0 ? `${fallback} (HTTP ${err.status})` : fallback;
  }
  if (err instanceof Error) {
    const msg = err.message?.trim();
    if (msg) return msg;
  }
  return fallback;
}

// formatRelative renders an ISO-8601 timestamp as a coarse "in N minutes" or
// "N minutes ago" string.
export function formatRelative(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const diff = date.getTime() - Date.now();
  const abs = Math.abs(diff);
  const suffix = diff < 0 ? "ago" : "from now";
  const minutes = Math.round(abs / 60_000);
  if (minutes < 1) return diff < 0 ? "just now" : "moments";
  if (minutes < 60) return `${minutes}m ${suffix}`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ${suffix}`;
  const days = Math.round(hours / 24);
  return `${days}d ${suffix}`;
}

// describeVerdictCode translates the short reason codes stored in event detail
// into honest one-line labels. The backend currently stores `refresh_expired`
// and `no_refresh_token` for verdicts the platform reached without contacting
// the IdP — surfacing the raw code reads as "the IdP returned this," which is
// incorrect. The IdP-returned code `invalid_grant` passes through verbatim
// because it IS what the IdP returned.
export function describeVerdictCode(code: string): string {
  switch (code) {
    case "refresh_expired":
      return "IdP-disclosed deadline reached";
    case "no_refresh_token":
      return "no refresh token stored";
    default:
      return code;
  }
}

// renderDetailHint produces a one-line detail string for an event, pulling the
// most relevant detail fields per type. Returns empty string when the row has
// nothing extra worth showing (a clean connect_started, for example).
export function renderDetailHint(ev: ConnectionAuthEvent): string {
  if (!ev.detail) return "";
  const d = ev.detail as Record<string, unknown>;
  if (typeof d.idp_error_code === "string" && d.idp_error_code) {
    return `(${describeVerdictCode(d.idp_error_code)})`;
  }
  if (typeof d.reason === "string" && d.reason) {
    return `(${describeVerdictCode(d.reason)})`;
  }
  if (d.rotated_refresh === true) {
    const ms = typeof d.duration_ms === "number" ? `, ${d.duration_ms}ms` : "";
    return `(rotated refresh${ms})`;
  }
  if (typeof d.duration_ms === "number" && d.duration_ms > 0) {
    return `(${d.duration_ms}ms)`;
  }
  return "";
}

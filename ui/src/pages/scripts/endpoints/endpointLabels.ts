import type { AdminEndpoint } from "@/api/admin/types";

// The words the Endpoints pages use for what the API reports (#2208).

export const ACCESS_LABELS: Record<AdminEndpoint["auth_mode"], string> = {
  public: "Public",
  signed_in: "Signed in",
};

export const REFUSAL_LABELS: Record<string, string> = {
  bot_check: "Bot check",
  rate_limit: "Endpoint rate limit",
  caller_rate_limit: "Caller rate limit",
  body_limit: "Body limit",
  credential: "Credential",
  origin: "Origin",
};

/** refusalLabel names a refusal reason, falling back to the reason itself. */
export function refusalLabel(reason: string): string {
  return REFUSAL_LABELS[reason] ?? reason;
}

/** formatMs is a duration in milliseconds as a table cell reads it. */
export function formatMs(ms: number): string {
  if (!ms) return "-";
  if (ms >= 1000) return `${(ms / 1000).toFixed(ms >= 10000 ? 0 : 1)} s`;
  return `${Math.round(ms)} ms`;
}

/** endpointPath is the page of one endpoint. */
export function endpointPath(name: string): string {
  return `/admin/automations/endpoints/${encodeURIComponent(name)}`;
}

/** ENDPOINTS_PATH is the Endpoints tab. */
export const ENDPOINTS_PATH = "/admin/automations/endpoints";

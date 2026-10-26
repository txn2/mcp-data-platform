import type { BotCheckProvider, EndpointAuthMode } from "@/api/admin/types";

// The words the Endpoints pages use for what the API reports (#2208, #2259).

export const ACCESS_LABELS: Record<EndpointAuthMode, string> = {
  authenticated: "Authenticated (API key or session)",
  public: "Public",
  bot_check: "Bot check",
};

/** accessLabel is an endpoint's access mode, with the check a bot_check one names. */
export function accessLabel(e: { auth_mode: EndpointAuthMode; bot_check?: string }): string {
  if (e.auth_mode === "bot_check" && e.bot_check) return `Bot check: ${e.bot_check}`;
  return ACCESS_LABELS[e.auth_mode] ?? e.auth_mode;
}

/** PROVIDER_LABELS names each bot check provider. */
export const PROVIDER_LABELS: Record<BotCheckProvider, string> = {
  pow: "Proof of work",
  turnstile: "Cloudflare Turnstile",
  hcaptcha: "hCaptcha",
};

/** PUBLIC_NOTE is how a public endpoint is marked wherever it is shown. */
export const PUBLIC_NOTE = "Answers anyone, with no check.";

/** callerLabel is one entry of an authenticated endpoint's callers list. */
export function callerLabel(entry: string): string {
  if (entry === "*") return "Every authenticated caller";
  if (entry.startsWith("persona:")) return `Persona ${entry.slice("persona:".length)}`;
  if (entry.startsWith("api_key:")) return `API key ${entry.slice("api_key:".length)}`;
  return entry;
}

/** callersSummary is an authenticated endpoint's callers list in one line. */
export function callersSummary(callers: string[]): string {
  if (callers.length === 0) return "Only the administrator: the callers list is empty";
  return callers.map(callerLabel).join(", ");
}

export const REFUSAL_LABELS: Record<string, string> = {
  bot_check: "Bot check",
  rate_limit: "Endpoint rate limit",
  caller_rate_limit: "Caller rate limit",
  body_limit: "Body limit",
  credential: "Credential",
  caller: "Caller not admitted",
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

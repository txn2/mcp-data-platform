import type {
  WebhookAuthMode,
  WebhookConfig,
  WebhookSource,
  WebhookSourceInput,
} from "@/api/admin/types";

// The form a webhook source is created and edited through (#1870). Every
// field is held as the text the input shows, so a number an administrator is
// halfway through typing is not coerced under them; toInput turns the form
// into the request body, sending a number only when one was entered and
// leaving the rest for the platform's defaults.

export interface WebhookForm {
  name: string;
  connection: string;
  persona: string;
  enabled: boolean;
  mode: WebhookAuthMode;
  secret: string;
  rotationOverlapHours: string;
  algorithm: string;
  signatureHeader: string;
  encoding: string;
  prefix: string;
  timestampHeader: string;
  toleranceSeconds: string;
  signed: string;
  header: string;
  username: string;
  handshake: string;
  split: string;
  eventIdPath: string;
  eventTypePath: string;
  keyPath: string;
  maxBodyBytes: string;
  flushMaxEvents: string;
  flushMaxIntervalMs: string;
  bufferLimit: string;
  rateLimitPerMinute: string;
  rateLimitBurst: string;
  compactEveryMinutes: string;
  rawRetentionDays: string;
  compactedRetentionDays: string;
}

export const EMPTY_FORM: WebhookForm = {
  name: "",
  connection: "",
  persona: "",
  enabled: true,
  mode: "hmac",
  secret: "",
  rotationOverlapHours: "24",
  algorithm: "sha256",
  signatureHeader: "",
  encoding: "hex",
  prefix: "",
  timestampHeader: "",
  toleranceSeconds: "",
  signed: "body",
  header: "",
  username: "",
  handshake: "none",
  split: "",
  eventIdPath: "",
  eventTypePath: "",
  keyPath: "",
  maxBodyBytes: "",
  flushMaxEvents: "",
  flushMaxIntervalMs: "",
  bufferLimit: "",
  rateLimitPerMinute: "",
  rateLimitBurst: "",
  compactEveryMinutes: "60",
  rawRetentionDays: "",
  compactedRetentionDays: "",
};

/** COMPACT_EVERY_MINUTES are the window lengths a source accepts: each divides
 * an hour, so windows start on the hour. */
export const COMPACT_EVERY_MINUTES = [1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30, 60] as const;

/** windowLength names a compact_every_minutes setting. */
export function windowLength(minutes: number): string {
  if (minutes === 60) return "Hour";
  return minutes === 1 ? "Minute" : `${minutes} minutes`;
}

/** NAME_PATTERN is the shape the platform accepts for a source name. */
export const NAME_PATTERN = /^[a-z][a-z0-9-]{0,62}$/;

const text = (n: number | undefined): string => (n === undefined || n === null ? "" : String(n));

/** fromSource fills the form from a stored source. The secret is never read
 * back, so it starts empty: left empty, the stored one is kept. */
export function fromSource(s: WebhookSource): WebhookForm {
  return {
    ...EMPTY_FORM,
    name: s.name,
    connection: s.connection,
    enabled: s.enabled,
    ...authFields(s.auth),
    ...configFields(s.config),
  };
}

function authFields(a: WebhookSource["auth"]): Partial<WebhookForm> {
  return {
    mode: a.mode,
    algorithm: a.algorithm ?? EMPTY_FORM.algorithm,
    signatureHeader: a.signature_header ?? "",
    encoding: a.encoding ?? EMPTY_FORM.encoding,
    prefix: a.prefix ?? "",
    timestampHeader: a.timestamp_header ?? "",
    toleranceSeconds: text(a.tolerance_seconds),
    signed: a.signed ?? EMPTY_FORM.signed,
    header: a.header ?? "",
    username: a.username ?? "",
  };
}

function configFields(c: WebhookConfig): Partial<WebhookForm> {
  return {
    persona: c.persona ?? "",
    handshake: c.handshake ?? EMPTY_FORM.handshake,
    split: c.split ?? "",
    eventIdPath: c.event_id_path ?? "",
    eventTypePath: c.event_type_path ?? "",
    keyPath: c.key_path ?? "",
    maxBodyBytes: text(c.max_body_bytes),
    flushMaxEvents: text(c.flush_max_events),
    flushMaxIntervalMs: text(c.flush_max_interval_ms),
    bufferLimit: text(c.buffer_limit),
    rateLimitPerMinute: text(c.rate_limit_per_minute),
    rateLimitBurst: text(c.rate_limit_burst),
    compactEveryMinutes: text(c.compact_every_minutes) || EMPTY_FORM.compactEveryMinutes,
    rawRetentionDays: text(c.raw_retention_days),
    compactedRetentionDays: text(c.compacted_retention_days),
  };
}

/** num reads a numeric field, or undefined when it is empty. */
function num(v: string): number | undefined {
  const t = v.trim();
  if (t === "") return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : undefined;
}

/** str reads a text field, or undefined when it is empty. */
function str(v: string): string | undefined {
  const t = v.trim();
  return t === "" ? undefined : t;
}

/** toInput renders the form as the request body. creating says whether the
 * name, connection and persona are sent: they are fixed once a source exists. */
export function toInput(f: WebhookForm, creating: boolean): WebhookSourceInput {
  const config: WebhookConfig = {
    handshake: f.handshake as WebhookConfig["handshake"],
    split: str(f.split),
    event_id_path: str(f.eventIdPath),
    event_type_path: str(f.eventTypePath),
    key_path: str(f.keyPath),
    max_body_bytes: num(f.maxBodyBytes),
    flush_max_events: num(f.flushMaxEvents),
    flush_max_interval_ms: num(f.flushMaxIntervalMs),
    buffer_limit: num(f.bufferLimit),
    rate_limit_per_minute: num(f.rateLimitPerMinute),
    rate_limit_burst: num(f.rateLimitBurst),
    compact_every_minutes: num(f.compactEveryMinutes),
    raw_retention_days: num(f.rawRetentionDays),
    compacted_retention_days: num(f.compactedRetentionDays),
  };
  const input: WebhookSourceInput = {
    enabled: f.enabled,
    auth: authOf(f),
    config,
  };
  if (creating) {
    input.name = f.name.trim();
    input.connection = f.connection;
    config.persona = str(f.persona);
  } else if (f.secret !== "") {
    input.rotation_overlap_seconds = Math.round((num(f.rotationOverlapHours) ?? 0) * 3600);
  }
  return input;
}

/** authOf sends only the settings the chosen mode reads. */
function authOf(f: WebhookForm): WebhookSourceInput["auth"] {
  const secret = f.secret === "" ? undefined : f.secret;
  switch (f.mode) {
    case "hmac":
      return {
        mode: "hmac",
        secret,
        algorithm: f.algorithm,
        signature_header: str(f.signatureHeader),
        encoding: f.encoding,
        prefix: str(f.prefix),
        timestamp_header: str(f.timestampHeader),
        tolerance_seconds: num(f.toleranceSeconds),
        signed: f.timestampHeader.trim() === "" ? "body" : f.signed,
      };
    case "header_token":
      return { mode: "header_token", secret, header: str(f.header) };
    case "basic":
      return { mode: "basic", secret, username: str(f.username) };
    default:
      return { mode: "path_token", secret };
  }
}

/** problems lists what stops the form being sent, in the words the reader
 * acts on. The platform checks everything again; these are the ones worth
 * saying before a round trip. */
export function problems(f: WebhookForm, creating: boolean): string[] {
  return [...(creating ? createProblems(f) : []), ...modeProblems(f)];
}

/** createProblems are the fields only a new source sets. */
function createProblems(f: WebhookForm): string[] {
  const out: string[] = [];
  const name = f.name.trim();
  if (!NAME_PATTERN.test(name)) {
    out.push("The name starts with a lowercase letter and holds only lowercase letters, digits and -.");
  } else if (name === "new") {
    out.push("The name new is reserved.");
  }
  if (f.connection === "") out.push("Choose the connection the source's table is created on.");
  if (f.secret === "") out.push("Enter the secret the sender uses.");
  return out;
}

/** modeProblems are the settings the chosen authentication mode needs. */
function modeProblems(f: WebhookForm): string[] {
  const needs: Partial<Record<WebhookAuthMode, [string, string]>> = {
    hmac: [f.signatureHeader, "Name the header the signature is sent in."],
    header_token: [f.header, "Name the header the token is sent in."],
    basic: [f.username, "Enter the username."],
  };
  const need = needs[f.mode];
  return need && need[0].trim() === "" ? [need[1]] : [];
}

export const MODE_LABELS: Record<WebhookAuthMode, string> = {
  hmac: "HMAC signature",
  header_token: "Token in a header",
  basic: "Basic credentials",
  path_token: "Token in the URL",
};

/** senderURL is the address to give the sender. A path_token source's token
 * is a further path segment, shown as a placeholder: the secret is never read
 * back. */
export function senderURL(origin: string, s: WebhookSource): string {
  const url = origin.replace(/\/$/, "") + s.path;
  return s.auth.mode === "path_token" ? `${url}/<token>` : url;
}

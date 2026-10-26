import type {
  WebhookAuthMode,
  WebhookConfig,
  WebhookInput,
  WebhookSource,
  WebhookSourceInput,
} from "@/api/admin/types";
import { EMPTY_IMAP, fromIMAP, imapOf, imapProblems, type IMAPForm } from "./imapForm";

// The form a webhook source is created and edited through (#1870). Every
// field is held as the text the input shows, so a number an administrator is
// halfway through typing is not coerced under them; toInput turns the form
// into the request body, sending a number only when one was entered and
// leaving the rest for the platform's defaults.

export interface WebhookForm {
  /** Where events come from: posted to the source, or a polled mailbox. */
  input: WebhookInput;
  imap: IMAPForm;
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
  idHeader: string;
  headerFormat: string;
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
  /** "default" leaves the choice to the deployment's resources.index rules. */
  index: string;
}

export const EMPTY_FORM: WebhookForm = {
  input: "http",
  imap: EMPTY_IMAP,
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
  idHeader: "",
  headerFormat: "",
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
  index: "default",
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
    input: s.config.input ?? "http",
    imap: fromIMAP(s.config.imap),
  };
}

function authFields(a: WebhookSource["auth"]): Partial<WebhookForm> {
  return {
    mode: a.mode,
    ...hmacFields(a),
    header: a.header ?? "",
    username: a.username ?? "",
  };
}

/** hmacFields are the HMAC settings of a stored source. */
function hmacFields(a: WebhookSource["auth"]): Partial<WebhookForm> {
  return {
    algorithm: a.algorithm ?? EMPTY_FORM.algorithm,
    signatureHeader: a.signature_header ?? "",
    encoding: a.encoding ?? EMPTY_FORM.encoding,
    prefix: a.prefix ?? "",
    timestampHeader: a.timestamp_header ?? "",
    toleranceSeconds: text(a.tolerance_seconds),
    signed: a.signed ?? EMPTY_FORM.signed,
    idHeader: a.id_header ?? "",
    headerFormat: a.header_format ?? "",
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
    index: c.index ?? "default",
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
  const config: WebhookConfig = configOf(f);
  const input: WebhookSourceInput = {
    enabled: f.enabled,
    auth: authOf(f),
    config,
  };
  if (creating) {
    input.name = f.name.trim();
    input.connection = f.connection;
    config.persona = str(f.persona);
  } else if (f.secret !== "" && f.input === "http") {
    input.rotation_overlap_seconds = Math.round((num(f.rotationOverlapHours) ?? 0) * 3600);
  }
  return input;
}

/** configOf is the config the source's input sends: a form's source sends
 * only what comes after the input, since the platform owns the rest. */
function configOf(f: WebhookForm): WebhookConfig {
  if (f.input === "form") return { input: "form", ...storageConfigOf(f) };
  return f.input === "imap" ? mailConfigOf(f) : httpConfigOf(f);
}

/** mailConfigOf is an imap source's config: the mailbox, and the settings
 * after the input that every source shares. A polled mailbox is not posted
 * to, so nothing about a request is sent. */
function mailConfigOf(f: WebhookForm): WebhookConfig {
  return {
    input: "imap",
    imap: imapOf(f.imap),
    ...storageConfigOf(f),
  };
}

/** storageConfigOf are the settings after the input: windows, retention, search. */
function storageConfigOf(f: WebhookForm): WebhookConfig {
  return {
    compact_every_minutes: num(f.compactEveryMinutes),
    raw_retention_days: num(f.rawRetentionDays),
    compacted_retention_days: num(f.compactedRetentionDays),
    index: f.index === "default" ? undefined : (f.index as WebhookConfig["index"]),
  };
}

function httpConfigOf(f: WebhookForm): WebhookConfig {
  return {
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
    ...storageConfigOf(f),
  };
}

/** authOf sends only the settings the chosen mode reads. */
function authOf(f: WebhookForm): WebhookSourceInput["auth"] {
  const secret = f.secret === "" ? undefined : f.secret;
  if (f.input === "form") return { mode: "" };
  if (f.input === "imap") {
    return { mode: "imap", secret, username: str(f.username) };
  }
  switch (f.mode) {
    case "hmac":
      return hmacAuthOf(f, secret);
    case "header_token":
      return { mode: "header_token", secret, header: str(f.header) };
    case "basic":
      return { mode: "basic", secret, username: str(f.username) };
    default:
      return { mode: "path_token", secret };
  }
}

/** hmacAuthOf sends the HMAC settings the header format reads: a Stripe
 * header carries its own timestamp, so it sends no prefix, timestamp header,
 * signed content or id header. */
function hmacAuthOf(f: WebhookForm, secret: string | undefined): WebhookSourceInput["auth"] {
  const common = {
    mode: "hmac" as const,
    secret,
    algorithm: f.algorithm,
    signature_header: str(f.signatureHeader),
    encoding: f.encoding,
    tolerance_seconds: num(f.toleranceSeconds),
  };
  if (f.headerFormat === "stripe") {
    return { ...common, header_format: "stripe" };
  }
  const signed = f.timestampHeader.trim() === "" ? "body" : f.signed;
  return {
    ...common,
    prefix: str(f.prefix),
    timestamp_header: str(f.timestampHeader),
    signed,
    id_header: signed === "id.timestamp.body" ? str(f.idHeader) : undefined,
  };
}

/** problems lists what stops the form being sent, in the words the reader
 * acts on. The platform checks everything again; these are the ones worth
 * saying before a round trip. */
export function problems(f: WebhookForm, creating: boolean): string[] {
  if (f.input === "form") return [];
  const input = f.input === "imap" ? imapProblems(f.imap, f.username) : modeProblems(f);
  return [...(creating ? createProblems(f) : []), ...input];
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
  if (f.secret === "") {
    out.push(f.input === "imap" ? "Enter the mailbox's password." : "Enter the secret the sender uses.");
  }
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
  const out = need && need[0].trim() === "" ? [need[1]] : [];
  if (f.mode === "hmac" && f.headerFormat !== "stripe" && f.timestampHeader.trim() !== "" &&
    f.signed === "id.timestamp.body" && f.idHeader.trim() === "") {
    out.push("Name the header the delivery id is sent in.");
  }
  return out;
}

export const MODE_LABELS: Record<WebhookAuthMode, string> = {
  hmac: "HMAC signature",
  header_token: "Token in a header",
  basic: "Basic credentials",
  path_token: "Token in the URL",
  imap: "Mailbox login",
};

/** HTTP_MODES are the modes an http source chooses between. */
export const HTTP_MODES: WebhookAuthMode[] = ["hmac", "header_token", "basic", "path_token"];

/** senderURL is the address to give the sender. A path_token source's token
 * is a further path segment, shown as a placeholder: the secret is never read
 * back. */
export function senderURL(origin: string, s: WebhookSource): string {
  if (s.config.input === "imap" || s.config.input === "form") return "";
  const url = origin.replace(/\/$/, "") + s.path;
  return s.auth.mode === "path_token" ? `${url}/<token>` : url;
}

import type { WebhookIMAP } from "@/api/admin/types";

// The mailbox half of the webhook form (#2247): an imap source polls a
// mailbox, and each message from an allowed sender becomes one event. Held as
// text like the rest of the form; lists are one entry per line.

export interface IMAPForm {
  host: string;
  port: string;
  tls: "tls" | "starttls";
  folders: string;
  allowSenders: string;
  requireAuthPass: boolean;
  trustedAuthServID: string;
  caBundle: string;
  pollSeconds: string;
  maxMessageBytes: string;
  maxAttachmentBytes: string;
}

export const EMPTY_IMAP: IMAPForm = {
  host: "",
  port: "",
  tls: "tls",
  folders: "INBOX",
  allowSenders: "",
  requireAuthPass: true,
  trustedAuthServID: "",
  caBundle: "",
  pollSeconds: "",
  maxMessageBytes: "",
  maxAttachmentBytes: "",
};

const text = (n: number | undefined): string => (n === undefined || n === null ? "" : String(n));

/** lines splits a one-entry-per-line field, dropping blank lines. */
export function lines(v: string): string[] {
  return v
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");
}

function num(v: string): number | undefined {
  const t = v.trim();
  if (t === "") return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : undefined;
}

/** fromIMAP fills the mailbox fields from a stored source. */
export function fromIMAP(m: WebhookIMAP | undefined): IMAPForm {
  if (!m) return EMPTY_IMAP;
  return {
    host: m.host,
    port: text(m.port),
    tls: m.tls ?? "tls",
    folders: (m.folders ?? []).join("\n"),
    allowSenders: (m.allow_senders ?? []).join("\n"),
    requireAuthPass: m.require_auth_pass ?? true,
    trustedAuthServID: m.trusted_authserv_id ?? "",
    caBundle: m.tls_ca_bundle_pem ?? "",
    pollSeconds: text(m.poll_seconds),
    maxMessageBytes: text(m.max_message_bytes),
    maxAttachmentBytes: text(m.max_attachment_bytes),
  };
}

/** imapOf renders the mailbox fields as the source's config.imap. */
export function imapOf(f: IMAPForm): WebhookIMAP {
  const trimmed = f.trustedAuthServID.trim();
  return {
    host: f.host.trim(),
    port: num(f.port),
    tls: f.tls,
    folders: lines(f.folders),
    allow_senders: lines(f.allowSenders),
    require_auth_pass: f.requireAuthPass,
    trusted_authserv_id: trimmed === "" ? undefined : trimmed,
    tls_ca_bundle_pem: f.caBundle.trim() === "" ? undefined : f.caBundle,
    poll_seconds: num(f.pollSeconds),
    max_message_bytes: num(f.maxMessageBytes),
    max_attachment_bytes: num(f.maxAttachmentBytes),
  };
}

/** imapProblems are the mailbox settings worth naming before a round trip. */
export function imapProblems(f: IMAPForm, username: string): string[] {
  const out: string[] = [];
  if (f.host.trim() === "") out.push("Enter the mail server's host.");
  if (username.trim() === "") out.push("Enter the mailbox's username.");
  if (lines(f.folders).length === 0) out.push("Name at least one folder to read.");
  if (lines(f.allowSenders).length === 0) {
    out.push("Name the addresses or @domains whose mail becomes events.");
  }
  if (f.requireAuthPass && f.trustedAuthServID.trim() === "") {
    out.push("Enter the receiving server's id, or stop requiring an authenticated sender.");
  }
  return out;
}

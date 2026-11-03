import type { WebhookPostgres } from "@/api/admin/types";
import { lines } from "./imapForm";

// The database half of the webhook form (#2408): a postgres source reads an
// application database, filing each notification on its channels, or each row
// of an outbox table, as one event. Held as text like the rest of the form;
// channels are one per line.

export interface PostgresForm {
  connection: string;
  mode: "notify" | "outbox";
  channels: string;
  table: string;
  pollSeconds: string;
  maxPayloadBytes: string;
}

export const EMPTY_POSTGRES: PostgresForm = {
  connection: "",
  mode: "outbox",
  channels: "",
  table: "public.outbox",
  pollSeconds: "",
  maxPayloadBytes: "",
};

const text = (n: number | undefined): string => (n === undefined || n === null ? "" : String(n));

function num(v: string): number | undefined {
  const t = v.trim();
  if (t === "") return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : undefined;
}

/** OUTBOX_TABLE is what an outbox's name may be: schema.table in lowercase. */
const OUTBOX_TABLE = /^[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*$/;

/** fromPostgres fills the database fields from a stored source. */
export function fromPostgres(p: WebhookPostgres | undefined): PostgresForm {
  if (!p) return EMPTY_POSTGRES;
  return {
    connection: p.connection,
    mode: p.mode,
    channels: (p.channels ?? []).join("\n"),
    table: p.table ?? "",
    pollSeconds: text(p.poll_seconds),
    maxPayloadBytes: text(p.max_payload_bytes),
  };
}

/** postgresOf renders the database fields as the source's config.postgres,
 * sending only what the chosen mode reads. */
export function postgresOf(f: PostgresForm): WebhookPostgres {
  const out: WebhookPostgres = { connection: f.connection, mode: f.mode, max_payload_bytes: num(f.maxPayloadBytes) };
  if (f.mode === "notify") {
    out.channels = lines(f.channels);
  } else {
    out.table = f.table.trim();
    out.poll_seconds = num(f.pollSeconds);
  }
  return out;
}

/** postgresProblems are the database settings worth naming before a round trip. */
export function postgresProblems(f: PostgresForm): string[] {
  const out: string[] = [];
  if (f.connection === "") out.push("Choose the application database the source reads.");
  if (f.mode === "notify" && lines(f.channels).length === 0) out.push("Name at least one channel to listen on.");
  if (f.mode === "outbox" && !OUTBOX_TABLE.test(f.table.trim())) {
    out.push("Enter the outbox as schema.table, in lowercase.");
  }
  return out;
}

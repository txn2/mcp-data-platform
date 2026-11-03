import type { WebhookListener, WebhookPostgres } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { relativeTime } from "@/components/provenance/parts";
import { Fact, type FactProps } from "./MailboxSection";

// DatabaseSection is a postgres source's database (#2408): what it reads,
// the replica listening, what it filed and refused, and its last failure.

export function DatabaseSection({ database, listener }: { database?: WebhookPostgres; listener?: WebhookListener }) {
  if (!database) return null;
  return (
    <SectionCard title="Database">
      <p className="mb-3 text-sm">{listeningText(listener)}</p>
      <dl className="grid gap-3 text-xs sm:grid-cols-3">
        {databaseFacts(database, listener).map((f) => (
          <Fact key={f.label} {...f} />
        ))}
      </dl>
      {listener?.last_error ? (
        <p className="mt-3 text-xs">
          Last failure{listener.last_error_at ? ` ${relativeTime(listener.last_error_at)}` : ""}:
          <span className="mt-1 block font-mono text-destructive">{listener.last_error}</span>
        </p>
      ) : null}
    </SectionCard>
  );
}

/** listeningText says who is listening and since when. */
function listeningText(l: WebhookListener | undefined): string {
  if (!l?.replica) return "No replica is listening.";
  return l.listening_since ? `Listening on ${l.replica} since ${relativeTime(l.listening_since)}.` : `Listening on ${l.replica}.`;
}

/** databaseFacts are the settings and counts an administrator checks a source against. */
function databaseFacts(p: WebhookPostgres, l: WebhookListener | undefined): FactProps[] {
  return [...settingFacts(p), ...listenerFacts(l)];
}

/** settingFacts are what the source reads and how. */
function settingFacts(p: WebhookPostgres): FactProps[] {
  if (p.mode === "notify") {
    return [
      { label: "Connection", value: p.connection, mono: true },
      { label: "Mode", value: "Notify" },
      { label: "Channels", value: (p.channels ?? []).join(", "), mono: true },
    ];
  }
  return [
    { label: "Connection", value: p.connection, mono: true },
    { label: "Mode", value: "Outbox" },
    { label: "Outbox table", value: p.table ?? "", mono: true },
    { label: "Poll interval", value: `${p.poll_seconds ?? 30} seconds` },
  ];
}

/** listenerFacts are what the listener filed and refused, and when it last filed. */
function listenerFacts(l: WebhookListener | undefined): FactProps[] {
  return [
    { label: "Last event", value: l?.last_event_at ? relativeTime(l.last_event_at) : "None yet" },
    { label: "Filed", value: String(l?.delivered ?? 0) },
    { label: "Refused", value: String(l?.rejected ?? 0) },
  ];
}

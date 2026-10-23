import type { WebhookIMAP, WebhookIMAPFolder, WebhookSource } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { relativeTime } from "@/components/provenance/parts";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

// MailboxSection is an imap source's mailbox (#2247): the server and login it
// checks, whose mail it keeps, and each folder as its last check left it:
// messages stored, and the ones the sender rules withheld, which are counted
// and never stored.

export function MailboxSection({
  source,
  folders,
  lastFiledAt,
}: {
  source: WebhookSource;
  folders: WebhookIMAPFolder[];
  /** When a message was last filed as an event; null when none has been. */
  lastFiledAt: string | null;
}) {
  const m = source.config.imap;
  if (!m) return null;
  return (
    <SectionCard title="Mailbox">
      <p className="mb-3 text-sm">
        {lastFiledAt ? (
          <>
            Last message filed <span className="font-medium">{relativeTime(lastFiledAt)}</span>.
          </>
        ) : (
          <>No message has been filed yet.</>
        )}
      </p>
      <dl className="grid gap-3 text-xs sm:grid-cols-3">
        {mailboxFacts(source, m).map((f) => (
          <Fact key={f.label} {...f} />
        ))}
      </dl>
      <Table className="mt-4">
        <TableHeader>
          <TableRow>
            <TableHead>Folder</TableHead>
            <TableHead className="text-right">Stored</TableHead>
            <TableHead className="text-right">Sender not allowed</TableHead>
            <TableHead className="text-right">Sender not authenticated</TableHead>
            <TableHead>Last checked</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {m.folders.map((name) => (
            <FolderRow key={name} folder={folders.find((f) => f.folder === name) ?? unchecked(name)} />
          ))}
        </TableBody>
      </Table>
    </SectionCard>
  );
}

interface FactProps {
  label: string;
  value: string;
  mono?: boolean;
}

/** mailboxFacts are the settings an administrator checks a source against. */
function mailboxFacts(source: WebhookSource, m: WebhookIMAP): FactProps[] {
  const tls = m.tls === "starttls" ? "STARTTLS" : "TLS";
  const authRequired = m.require_auth_pass ?? true;
  return [
    { label: "Server", value: `${m.host}:${m.port ?? ""} (${tls})`, mono: true },
    { label: "Username", value: source.auth.username ?? "", mono: true },
    { label: "Password", value: source.auth.secret_set ? "Set" : "Not set" },
    { label: "Allowed senders", value: m.allow_senders.join(", "), mono: true },
    {
      label: "Authenticated sender",
      value: authRequired ? `Required, read from ${m.trusted_authserv_id ?? ""}` : "Not required",
    },
    { label: "Checked every", value: `${m.poll_seconds ?? 60} seconds` },
  ];
}

/** unchecked is a folder no check has reached yet. */
function unchecked(name: string): WebhookIMAPFolder {
  return { folder: name, uid_validity: 0, last_uid: 0, stored: 0, hidden_not_allowed: 0, hidden_unauthenticated: 0 };
}

function FolderRow({ folder }: { folder: WebhookIMAPFolder }) {
  return (
    <TableRow>
      <TableCell className="font-mono text-xs">{folder.folder}</TableCell>
      <TableCell className="text-right tabular-nums">{folder.stored}</TableCell>
      <TableCell className="text-right tabular-nums">{folder.hidden_not_allowed}</TableCell>
      <TableCell className="text-right tabular-nums">{folder.hidden_unauthenticated}</TableCell>
      <TableCell className="text-xs">
        {folder.last_polled_at ? relativeTime(folder.last_polled_at) : "Not yet"}
        {folder.last_error ? <span className="mt-1 block font-mono text-destructive">{folder.last_error}</span> : null}
      </TableCell>
    </TableRow>
  );
}

function Fact({ label, value, mono }: FactProps) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={mono ? "mt-0.5 font-mono break-all" : "mt-0.5"}>{value || "-"}</dd>
    </div>
  );
}

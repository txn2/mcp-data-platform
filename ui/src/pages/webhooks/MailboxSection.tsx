import type { WebhookAttachmentRepair, WebhookIMAP, WebhookIMAPFolder, WebhookSource } from "@/api/admin/types";
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
  repair,
}: {
  source: WebhookSource;
  folders: WebhookIMAPFolder[];
  /** When a message was last filed as an event; null when none has been. */
  lastFiledAt: string | null;
  /** What the one-time attachment repair did (#2836), absent until it ran. */
  repair?: WebhookAttachmentRepair;
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
      {repair && <AttachmentRepair source={source.name} repair={repair} />}
    </SectionCard>
  );
}

/**
 * AttachmentRepair says what the repair of the source's attachments did
 * (#2836, #2842), when it did anything: the folders it moved to their new
 * addresses, the later copies it removed, the copies of a message filed again
 * under another numbering of its folder, and the folders it kept, with the
 * first of each list behind a disclosure.
 */
export function AttachmentRepair({ source, repair }: { source: string; repair: WebhookAttachmentRepair }) {
  const { unmoved, failed } = leftInPlace(repair);
  if (repair.moved + repair.removed + repair.collapsed + repair.kept + unmoved + failed === 0) return null;
  const n = (v: number) => v.toLocaleString();
  const at = new Date(repair.repaired_at).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
  const root = `webhooks/${source}/attachments/`;
  const short = (p: string) => (p.startsWith(root) ? p.slice(root.length) : p);
  const files = (k: number) => `${n(k)} file${k === 1 ? "" : "s"}`;
  return (
    <div className="mt-4 text-sm" data-testid="attachment-repair">
      <p>
        Attachments repaired {at}: {n(repair.moved)} message folder{repair.moved === 1 ? "" : "s"} moved to their new addresses,{" "}
        {n(repair.removed)} later cop{repair.removed === 1 ? "y" : "ies"} removed ({files(repair.removed_files)}),{" "}
        {n(repair.collapsed)} renumbered cop{repair.collapsed === 1 ? "y" : "ies"} removed ({files(repair.collapsed_files)}), {n(repair.kept)}{" "}
        folder{repair.kept === 1 ? "" : "s"} kept (no message names them and they have no copy)
        <LeftInPlace unmoved={unmoved} failed={failed} />.
      </p>
      {repair.removed + repair.collapsed + repair.kept + unmoved + failed > 0 && (
        <details className="mt-2">
          <summary className="cursor-pointer text-xs text-muted-foreground">Show folders</summary>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 font-mono text-xs">
            <RepairList label="Removed" total={repair.removed} items={repair.report.removed.map((r) => `${short(r.folder)} (${files(r.files)})`)} />
            <RepairList
              label="Renumbered"
              total={repair.collapsed}
              items={repair.report.collapsed.map((r) => `${short(r.folder)} (${files(r.files)})`)}
            />
            <RepairList label="Kept" total={repair.kept} items={repair.report.kept.map(short)} />
            <RepairList label="Not moved" total={unmoved} items={(repair.report.unmoved ?? []).map(short)} />
            <FailedList total={failed} items={(repair.report.failed ?? []).map((f) => ({ folder: short(f.folder), error: f.error }))} />
          </dl>
        </details>
      )}
    </div>
  );
}

function RepairList({ label, total, items }: { label: string; total: number; items: string[] }) {
  if (total === 0) return null;
  return (
    <>
      <dt className="font-sans text-muted-foreground">{label}</dt>
      <dd>
        {items.map((i) => (
          <span key={i} className="block break-all">
            {i}
          </span>
        ))}
        {total > items.length && <span className="block font-sans text-muted-foreground">and {(total - items.length).toLocaleString()} more</span>}
      </dd>
    </>
  );
}

/** How many folders the repair left where they were (#2845), none in a record from before it. */
function leftInPlace(repair: WebhookAttachmentRepair): { unmoved: number; failed: number } {
  return { unmoved: repair.unmoved ?? 0, failed: repair.failed ?? 0 };
}

/** The end of the repair's sentence: the folders it left unmoved and the ones it failed on (#2845). */
function LeftInPlace({ unmoved, failed }: { unmoved: number; failed: number }) {
  const s = (k: number) => (k === 1 ? "" : "s");
  return (
    <>
      {unmoved > 0 &&
        `, ${unmoved.toLocaleString()} folder${s(unmoved)} not moved (a message filed twice, whose folder already holds the first copy)`}
      {failed > 0 && `, ${failed.toLocaleString()} folder${s(failed)} that couldn't be repaired`}
    </>
  );
}

/** The folders the repair could not move or remove, each with what the library said (#2845). */
function FailedList({ total, items }: { total: number; items: { folder: string; error: string }[] }) {
  if (total === 0) return null;
  return (
    <>
      <dt className="font-sans text-muted-foreground">Failed</dt>
      <dd>
        {items.map((f) => (
          <span key={f.folder} className="block break-all">
            {f.folder}
            <span className="block font-sans whitespace-normal text-destructive">{f.error}</span>
          </span>
        ))}
        {total > items.length && <span className="block font-sans text-muted-foreground">and {(total - items.length).toLocaleString()} more</span>}
      </dd>
    </>
  );
}

export interface FactProps {
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

export function Fact({ label, value, mono }: FactProps) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={mono ? "mt-0.5 font-mono break-all" : "mt-0.5"}>{value || "-"}</dd>
    </div>
  );
}

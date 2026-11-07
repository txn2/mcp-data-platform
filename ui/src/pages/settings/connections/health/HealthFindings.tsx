import { useState } from "react";
import { AlertTriangle, Check, CircleAlert, Info } from "lucide-react";
import { useAppDBSignal, type AppDBFinding, type AppDBHealthReport, type AppDBSession } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

// The findings the Health card lists (#2528), most severe first: each a card
// with its icon, a title naming the thing and the fact, a sentence on why it
// matters with its threshold, and its action — a session's cancel and end, an
// index's way to its migrations. The checks that passed follow as one line.

const ICON = {
  critical: { icon: CircleAlert, className: "text-red-500" },
  warning: { icon: AlertTriangle, className: "text-amber-500" },
  info: { icon: Info, className: "text-blue-500" },
} as const;

const INDEX_KINDS = new Set(["unused_index", "duplicate_index", "invalid_index"]);
const SESSION_KINDS = new Set(["idle_in_transaction", "long_running"]);

export function Findings({
  r,
  connection,
  heading,
}: {
  r: AppDBHealthReport;
  connection: string;
  heading: string;
}) {
  const signal = useAppDBSignal(connection);
  const sessions = new Map(r.sessions.map((s) => [s.pid, s]));
  return (
    <section className="space-y-2" aria-labelledby="health-findings-h">
      <h3 id="health-findings-h" className="text-sm font-semibold">
        {heading}
      </h3>
      {signal.error && (
        <Alert variant="destructive">
          <AlertDescription>{signal.error.message}</AlertDescription>
        </Alert>
      )}
      <ul className="space-y-2">
        {r.findings.map((f, i) => (
          <FindingCard
            key={`${f.kind}-${f.pid ?? f.index ?? f.table ?? i}`}
            finding={f}
            session={f.pid !== undefined && SESSION_KINDS.has(f.kind) ? sessions.get(f.pid) : undefined}
            busy={signal.isPending}
            onSignal={(pid, kind) => signal.mutate({ pid, signal: kind })}
          />
        ))}
      </ul>
    </section>
  );
}

function FindingCard({
  finding: f,
  session,
  busy,
  onSignal,
}: {
  finding: AppDBFinding;
  session: AppDBSession | undefined;
  busy: boolean;
  onSignal: (pid: number, kind: "cancel" | "terminate") => void;
}) {
  const [ending, setEnding] = useState(false);
  const { icon: Icon, className } = ICON[f.level];
  return (
    <li className="flex gap-3 rounded-xl border p-4" data-testid={`health-finding-${f.kind}`} data-level={f.level}>
      <Icon aria-label={f.level} className={cn("mt-0.5 size-5 shrink-0", className)} />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            <p className="text-sm font-medium">{f.title}</p>
            <p className="text-[13px] text-muted-foreground">{f.detail}</p>
          </div>
          {session && (
            <div className="flex shrink-0 gap-2">
              <Button size="sm" variant="outline" disabled={busy} onClick={() => onSignal(session.pid, "cancel")}>
                Cancel query
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="border-destructive/50 text-destructive hover:bg-destructive/10"
                disabled={busy}
                onClick={() => setEnding(true)}
              >
                End session
              </Button>
            </div>
          )}
          {INDEX_KINDS.has(f.kind) && (
            <button
              type="button"
              className="shrink-0 text-[13px] font-medium text-primary hover:underline"
              onClick={() => document.getElementById("appdb-migrations")?.scrollIntoView({ behavior: "smooth", block: "start" })}
            >
              Drop it in a migration
            </button>
          )}
        </div>
        {session && (
          <>
            <dl className="flex flex-wrap gap-x-5 gap-y-1 text-xs">
              <Fact label="Role" value={session.user} />
              {session.application && <Fact label="Client" value={session.application} />}
              <Fact label="Process" value={String(session.pid)} />
            </dl>
            {session.query && (
              <p className="truncate rounded-md bg-muted/50 px-2 py-1 font-mono text-xs" title={session.query}>
                {session.query}
              </p>
            )}
          </>
        )}
      </div>
      {ending && session && (
        <Dialog open onOpenChange={(open) => !open && setEnding(false)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>End session</DialogTitle>
              <DialogDescription>
                End process {session.pid}? Its open transaction is rolled back and the client is disconnected.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button variant="ghost" onClick={() => setEnding(false)}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  onSignal(session.pid, "terminate");
                  setEnding(false);
                }}
              >
                End session
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </li>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-1.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-medium">{value}</dd>
    </div>
  );
}

/** PassedChecks is one line of what the report found nothing wrong with. */
export function PassedChecks({ checks }: { checks: string[] }) {
  if (checks.length === 0) return null;
  return (
    <ul className="flex flex-wrap gap-x-5 gap-y-1.5 text-[13px] text-muted-foreground" data-testid="health-passed">
      {checks.map((c) => (
        <li key={c} className="flex items-center gap-1.5">
          <Check aria-hidden="true" className="size-3.5 text-emerald-500" />
          {c}
        </li>
      ))}
    </ul>
  );
}

import { useState } from "react";
import { ChevronRight, History } from "lucide-react";
import type {
  ApplicationDetail,
  ApplicationPinChange,
  ApplicationRelease,
} from "@/api/portal/hooks/applications";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { ReleaseControls } from "./ApplicationReleaseControls";
import { ACTION_LABEL, PART_KIND_LABEL, formatWhen, versionLabel } from "./applicationFormat";

// ApplicationReleases is an application's release history, newest first
// (#2411). Each entry says what it was (a release, a roll back, an import),
// who made it and when, and opens to the changes it made: each part's pin from
// one version to another, the migrations it applied or reverted, and the note
// a roll back leaves about migrations it kept. Above the history, an
// administrator reads what a release now would change and what would stop it.

interface Props {
  detail: ApplicationDetail;
  isAdmin: boolean;
}

export function ApplicationReleases({ detail, isAdmin }: Props) {
  return (
    <SectionCard
      title="Releases"
      action={isAdmin && <ReleaseControls detail={detail} />}
    >
      <div className="space-y-3">
        <PendingSummary detail={detail} />
        {detail.releases.length === 0 ? (
          <EmptyState icon={History}>No releases</EmptyState>
        ) : (
          <ul className="divide-y rounded-md border">
            {detail.releases.map((r) => (
              <ReleaseEntry
                key={r.number}
                release={r}
                current={r.number === detail.application.current_release}
              />
            ))}
          </ul>
        )}
      </div>
    </SectionCard>
  );
}

// PendingSummary is what a release now would do, and what the server would
// refuse it for.
function PendingSummary({ detail }: { detail: ApplicationDetail }) {
  const pendingMigrations = detail.migrations?.pending ?? [];
  if (detail.pending.length === 0 && pendingMigrations.length === 0 && detail.problems.length === 0) {
    return null;
  }
  return (
    <div className="space-y-2" data-testid="application-pending">
      {detail.problems.length > 0 && (
        <Alert variant="destructive">
          <AlertDescription>
            <ul className="list-disc pl-4">
              {detail.problems.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {(detail.pending.length > 0 || pendingMigrations.length > 0) && (
        <div className="rounded-md border bg-muted/40 p-3 text-sm">
          <div className="mb-1 text-xs font-medium text-muted-foreground">Pending</div>
          <PinChangeList changes={detail.pending} />
          {pendingMigrations.length > 0 && (
            <p className="mt-1">Migrations {pendingMigrations.join(", ")}</p>
          )}
        </div>
      )}
    </div>
  );
}

function ReleaseEntry({ release, current }: { release: ApplicationRelease; current: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <li className="p-3" data-testid={`release-${release.number}`}>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <span className="font-medium">{release.number}</span>
        <Badge variant={release.action === "rollback" ? "warning" : "muted"}>
          {ACTION_LABEL[release.action] ?? release.action}
        </Badge>
        {release.restored ? (
          <span className="text-muted-foreground">to {release.restored}</span>
        ) : null}
        {current && <Badge variant="info">Current</Badge>}
        <span className="text-muted-foreground">
          {release.created_by} · {formatWhen(release.created_at)}
        </span>
      </div>
      {release.note && <p className="mt-1 text-sm">{release.note}</p>}
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="mt-2 inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
      >
        <ChevronRight aria-hidden className={cn("size-3 transition-transform", open && "rotate-90")} />
        Changes
      </button>
      {open && <ReleaseChanges release={release} />}
    </li>
  );
}

function ReleaseChanges({ release }: { release: ApplicationRelease }) {
  const c = release.changes;
  const nothing =
    c.parts.length === 0 && c.applied.length === 0 && c.reverted.length === 0 && !c.migrations_kept;
  return (
    <div className="mt-2 space-y-1 rounded-md bg-muted/40 p-3 text-sm" data-testid={`changes-${release.number}`}>
      {nothing && <p className="text-muted-foreground">No changes</p>}
      <PinChangeList changes={c.parts} />
      {c.applied.length > 0 && <p>Applied migrations {c.applied.join(", ")}</p>}
      {c.reverted.length > 0 && <p>Reverted migrations {c.reverted.join(", ")}</p>}
      {c.migrations_kept && <p className="text-muted-foreground">{c.migrations_kept}</p>}
    </div>
  );
}

function PinChangeList({ changes }: { changes: ApplicationPinChange[] }) {
  if (changes.length === 0) return null;
  return (
    <ul className="space-y-0.5">
      {changes.map((ch) => (
        <li key={`${ch.kind}/${ch.part_id}`}>
          <span className="text-muted-foreground">{PART_KIND_LABEL[ch.kind] ?? ch.kind}</span>{" "}
          <span className="font-mono text-xs">{ch.part_id}</span>{" "}
          {versionLabel(ch.from)} → {versionLabel(ch.to)}
        </li>
      ))}
    </ul>
  );
}

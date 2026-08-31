import { Bot, FileCog, User } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import {
  useProducers,
  type Producer,
  type ProducedTargetKind,
} from "@/api/portal/hooks/producers";
import { SectionCard } from "@/components/patterns/SectionCard";
import { relativeTime } from "@/components/provenance/parts";
import { Badge } from "@/components/ui/badge";

// ProducersPanel lists what wrote this file (#1569): the scripts, sessions and
// people that created or modified it, most recent writer first.
//
// It answers a different question from an asset's provenance, which sits beside
// it and records the data calls the CONTENT was built from. This records who
// did the building, and it is the half a reader could not see before: an hourly
// script and the person who edits the same report both appear, so "who else
// writes to this file" and "what goes stale if I retire this script" have an
// answer on the page rather than in a run history nobody reads.
//
// One component serves both kinds, because it is one relation read from one
// end whichever kind of file the reader is standing on.
export function ProducersPanel({
  target,
  scriptPath,
  sessionPath,
  onNavigate,
  className,
}: {
  target: { kind: ProducedTargetKind; id: string };
  /** Where a producing script opens for this reader. Absent, the row names the
   * script without linking to it. */
  scriptPath?: (scriptId: string) => string;
  /** Where a producing session opens for this reader. */
  sessionPath?: (sessionId: string) => string;
  onNavigate?: (path: string) => void;
  /** Wrapper classes for the surface this sits on -- the asset sidebar rules a
   * line above each of its panels. It is on the section rather than around it,
   * so a file with no recorded producer leaves no stray divider behind. */
  className?: string;
}) {
  const { data, isError } = useProducers(target.kind, target.id);
  const producers = data?.data ?? [];

  // A file written before this shipped has no recorded producer, and saying
  // "nothing wrote this" would be a lie about a file that plainly exists.
  if (isError || producers.length === 0) return null;

  return (
    <div className={className}>
      <SectionCard data-testid="producers-panel" title={`Written by (${producers.length})`}>
        <ul className="space-y-2">
          {producers.map((p) => (
            <li key={`${p.kind}:${p.id}`}>
              <ProducerRow
                producer={p}
                href={hrefFor(p, scriptPath, sessionPath)}
                onNavigate={onNavigate}
              />
            </li>
          ))}
        </ul>
      </SectionCard>
    </div>
  );
}

// hrefFor is where a producer opens, or undefined when it cannot be opened: a
// person has no page, and neither does a script that no longer exists.
function hrefFor(
  p: Producer,
  scriptPath?: (id: string) => string,
  sessionPath?: (id: string) => string,
): string | undefined {
  if (p.kind === "script") return p.exists && scriptPath ? scriptPath(p.id) : undefined;
  if (p.kind === "session") return sessionPath ? sessionPath(p.id) : undefined;
  return undefined;
}

const KIND_ICONS: Record<Producer["kind"], LucideIcon> = {
  script: FileCog,
  session: Bot,
  person: User,
};

function ProducerRow({
  producer,
  href,
  onNavigate,
}: {
  producer: Producer;
  href?: string;
  onNavigate?: (path: string) => void;
}) {
  const Icon = KIND_ICONS[producer.kind];
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <div className="flex min-w-0 items-center gap-2">
        <Icon aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
        {href && onNavigate ? (
          <button
            type="button"
            onClick={() => onNavigate(href)}
            className="min-w-0 truncate text-left text-xs text-primary hover:underline"
          >
            {displayName(producer)}
          </button>
        ) : (
          <span className="min-w-0 truncate text-xs">{displayName(producer)}</span>
        )}
        <Badge variant={producer.created ? "default" : "secondary"} className="shrink-0 text-[10px]">
          {producer.created ? "created" : "modified"}
        </Badge>
      </div>
      <p className="pl-5.5 text-xs text-muted-foreground">{writeSummary(producer)}</p>
      {producer.kind === "script" && !producer.exists && (
        <p className="pl-5.5 text-xs text-muted-foreground">
          This script no longer exists. What it wrote is no longer being refreshed.
        </p>
      )}
    </div>
  );
}

// displayName is what to call a producer. A session has no name of its own, so
// it is named by a short form of its id, the way every other surface names one.
function displayName(p: Producer): string {
  if (p.label) return p.label;
  if (p.kind === "session") return `Session ${p.id.slice(0, 12)}`;
  return p.id;
}

// writeSummary is the one line under a producer: how many times it wrote and
// when it last did. The count is what separates a script that refreshes this
// file hourly from one that touched it once.
function writeSummary(p: Producer): string {
  const times = p.write_count === 1 ? "1 write" : `${p.write_count} writes`;
  return `${times}, last ${relativeTime(p.last_write_at)}`;
}

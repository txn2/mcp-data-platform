import {
  type CallerPersonaShare,
  type CallerShare,
  type TopCallers,
  useTopCallers,
} from "@/api/admin/indexjobs";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/patterns/EmptyState";
import { relTime } from "./helpers";
import { Section } from "./panels";

// TopCallersPanel names who wrote the call catalog, largest share first, so an
// operator sees an automated caller holding most of it before the calls index
// does (#1980). Each persona links to its editor, where Service account is the
// one setting that stops its calls being recorded.

// shareText renders a share of the catalog as a percentage with one decimal,
// floored so a caller just short of the whole never reads as all of it.
export function shareText(share: number): string {
  const tenths = Math.floor(share * 1000) / 10;
  return `${tenths.toFixed(1)}%`;
}

// personaHref is the persona editor with the persona open.
export function personaHref(persona: string): string {
  return `/portal/admin/personas?persona=${encodeURIComponent(persona)}`;
}

// PersonaMark says a persona's calls are no longer added to Calls. A persona
// named in calls.exclude_personas is treated the same as one marked in its
// editor, so it reads the same; only where it is changed differs.
function PersonaMark({ row }: { row: CallerPersonaShare }) {
  if (row.service_account) {
    return <Badge variant="muted">Service account</Badge>;
  }
  if (row.excluded_by_config) {
    return (
      <Badge variant="muted" title="Set in the configuration file (calls.exclude_personas)">
        Service account
      </Badge>
    );
  }
  return null;
}

// PersonaLink is a persona's name, linked to its editor. A call made with no
// persona has no editor to open.
function PersonaLink({ persona }: { persona: string }) {
  if (!persona) {
    return <span className="text-muted-foreground">no persona</span>;
  }
  return (
    <a href={personaHref(persona)} className="font-mono text-xs text-primary hover:underline">
      {persona}
    </a>
  );
}

function CallerRow({ row }: { row: CallerShare }) {
  return (
    <li className="flex items-center justify-between gap-2 text-sm">
      <span className="flex min-w-0 flex-col">
        <span className="truncate font-mono text-xs">{row.user_email || row.user_id}</span>
        <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
          as <PersonaLink persona={row.persona} /> <PersonaMark row={row} />
        </span>
      </span>
      <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
        {row.records.toLocaleString()} · {shareText(row.share)}
      </span>
    </li>
  );
}

function PersonaRow({ row }: { row: CallerPersonaShare }) {
  return (
    <li className="flex items-center justify-between gap-2 text-sm">
      <span className="flex min-w-0 items-center gap-1.5">
        <PersonaLink persona={row.persona} /> <PersonaMark row={row} />
      </span>
      <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
        {row.records.toLocaleString()} · {shareText(row.share)}
      </span>
    </li>
  );
}

export function TopCallersPanel({ data, isError }: { data?: TopCallers; isError: boolean }) {
  if (isError) {
    return <EmptyState className="py-6">Could not count the callers.</EmptyState>;
  }
  if (!data) {
    return <EmptyState className="py-6">Counting callers…</EmptyState>;
  }
  if (data.total === 0) {
    return <EmptyState className="py-6">No calls recorded.</EmptyState>;
  }
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <div>
        <h4 className="mb-2 text-xs font-medium text-muted-foreground">Callers</h4>
        <ul className="space-y-2">
          {data.principals.map((row) => (
            <CallerRow key={`${row.user_id}\u0000${row.persona}`} row={row} />
          ))}
        </ul>
      </div>
      <div>
        <h4 className="mb-2 text-xs font-medium text-muted-foreground">Personas</h4>
        <ul className="space-y-2">
          {data.personas.map((row) => (
            <PersonaRow key={row.persona} row={row} />
          ))}
        </ul>
      </div>
    </div>
  );
}

// TopCallersSection is the Indexing page's Top callers panel, which reads its
// own count so the page asks for it only when it shows it.
export function TopCallersSection() {
  const q = useTopCallers();
  const hint = q.data
    ? `share of ${q.data.total.toLocaleString()} recorded calls · counted ${relTime(q.data.counted_at)}`
    : "share of recorded calls";
  return (
    <Section title="Top callers" hint={hint}>
      <TopCallersPanel data={q.data} isError={q.isError} />
    </Section>
  );
}

import type { ApplicationFinding } from "@/api/portal/hooks/applications";
import { Badge } from "@/components/ui/badge";

/**
 * DiagramFindings is what the diagram finds wrong; clicking one selects the
 * node it is about. Which automations write a table named only when they run
 * is said once, under the findings it bears on, rather than as one more
 * finding (#2699, #2700).
 */
export function DiagramFindings({ findings, onSelect }: { findings: ApplicationFinding[]; onSelect: (id: string) => void }) {
  if (findings.length === 0) return null;
  const listed = findings.filter((f) => f.kind !== "runtime_writes");
  const notes = findings.filter((f) => f.kind === "runtime_writes");
  return (
    <section aria-label="Findings" className="rounded-md border bg-muted/40 p-3" data-testid="diagram-findings">
      <h4 className="mb-1.5 text-xs font-medium text-muted-foreground">
        Findings{listed.length > 1 ? ` · ${listed.length}` : ""}
      </h4>
      <ul className="space-y-1.5">
        {listed.map((f, i) => (
          <li key={`${f.node}-${i}`}>
            <button
              type="button"
              className="flex w-full items-start gap-2 text-left text-sm hover:underline"
              onClick={() => onSelect(f.node)}
            >
              <span className="min-w-0 flex-1">{f.message}</span>
              {f.blocks && <Badge variant="danger">Blocks release</Badge>}
            </button>
          </li>
        ))}
      </ul>
      {notes.map((f, i) => (
        <p key={`note-${i}`} className="mt-1.5 text-xs text-muted-foreground" data-testid="diagram-runtime-writes">
          {f.message}
        </p>
      ))}
    </section>
  );
}

import { useState } from "react";
import type { CallHandlerDetail, HandlerFailure } from "@/api/portal/hooks/callHandlers";
import { useSetHandlerEnabled } from "@/api/portal/hooks/scriptHandlers";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/stores/auth";
import { formatWhen } from "../runFormat";
import { CallHandlerForm } from "./CallHandlerForm";
import { appliesTo, conditionsText, operationsText, versionText } from "./format";

// CallHandlerPanel is one tool-call handler opened on its script's page
// (#2436): what it matches and does, which version runs, and, for an
// administrator, its controls and the failures it kept. Its owner, when not
// an administrator, reads it and changes nothing: an administrator adds,
// changes and turns these on and off.

export function CallHandlerPanel({ handler: h, contract }: { handler: CallHandlerDetail; contract: ScriptContract }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [editing, setEditing] = useState(false);
  const toggle = useSetHandlerEnabled(h.handler);
  if (editing) {
    return (
      <CallHandlerForm
        scriptId={contract.id}
        kind={h.kind}
        handler={h}
        approval={{ latest: contract.version, running: h.running_version ?? 0 }}
        onDone={() => setEditing(false)}
      />
    );
  }
  return (
    <div className="space-y-3 text-sm" data-testid="call-handler-panel">
      <dl className="grid grid-cols-1 gap-x-3 gap-y-1 sm:grid-cols-[140px_minmax(0,1fr)]">
        {factsOf(h, contract.version).map(([term, value, mono]) => (
          <Fact key={term} term={term} value={value} mono={mono} />
        ))}
      </dl>
      {isAdmin ? (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
            Edit
          </Button>
          <Button size="sm" variant="outline" disabled={toggle.isPending} onClick={() => toggle.mutate(!h.enabled)}>
            {h.enabled ? "Turn off" : "Turn on"}
          </Button>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">An administrator adds, changes and turns these on and off.</p>
      )}
      {toggle.error && (
        <Alert variant="destructive">
          <AlertDescription>{toggle.error.message}</AlertDescription>
        </Alert>
      )}
      {isAdmin && <Failures failures={h.recent_failures ?? []} />}
    </div>
  );
}

// factsOf are what a handler matches and does, the ones it has.
function factsOf(h: CallHandlerDetail, latest: number): [string, string, boolean][] {
  const facts: [string, string, boolean][] = [
    ["Connection", h.connection, false],
    ["Applies to", appliesTo(h), false],
    ["Operations", operationsText(h), true],
  ];
  if (h.conditions.length > 0) facts.push(["Only when", conditionsText(h), true]);
  if (h.kind === "policy" && h.items_path) facts.push(["Items", h.items_path, true]);
  const protects = h.protected ?? [];
  if (h.kind === "transform" && protects.length > 0) facts.push(["Protects", protects.join(", "), true]);
  if (h.kind !== "watch") facts.push(["Time limit", `${h.timeout_ms.toLocaleString()} ms; on failure ${failureWords(h.on_failure)}`, false]);
  facts.push(["Version", versionText(h, latest), false]);
  return facts;
}

function Fact({ term, value, mono }: { term: string; value: string; mono: boolean }) {
  return (
    <>
      <dt className="text-muted-foreground">{term}</dt>
      <dd className={mono ? "min-w-0 break-words font-mono text-xs" : "min-w-0 break-words"}>{value}</dd>
    </>
  );
}

function failureWords(mode: string): string {
  switch (mode) {
    case "hold":
      return "the call is held for an administrator";
    case "deny":
      return "the call is refused";
    case "original":
      return "the original result is returned";
    default:
      return "the call fails, naming this handler";
  }
}

// Failures are the decisions or changes that failed, with what the script was
// given and printed, kept 7 days. A stored secret appears as its placeholder.
function Failures({ failures }: { failures: HandlerFailure[] }) {
  if (failures.length === 0) {
    return <p className="text-xs text-muted-foreground">No failures in the last 7 days.</p>;
  }
  return (
    <div className="space-y-2" data-testid="handler-failures">
      <p className="text-sm font-medium">Failures, kept 7 days</p>
      {failures.map((f) => (
        <details key={f.id} className="rounded-md border p-2 text-xs">
          <summary className="cursor-pointer">
            {formatWhen(f.at)} · {f.user || "a script"} · <span className="text-destructive">{f.error}</span>
          </summary>
          <div className="mt-2 grid grid-cols-1 gap-2 lg:grid-cols-2">
            <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-words rounded border p-2 font-mono">
              {JSON.stringify(f.call, null, 2)}
            </pre>
            <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-words rounded border p-2 font-mono">
              {f.prints || "Printed nothing."}
            </pre>
          </div>
        </details>
      ))}
    </div>
  );
}

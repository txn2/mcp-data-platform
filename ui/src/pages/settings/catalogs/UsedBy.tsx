import type { APICaller } from "@/api/admin/hooks";
import { useOperationUsedBy } from "@/api/admin/hooks";

// What uses an operation (#2485): scripts whose saved source names it, scripts
// a run of which called it, the endpoints bound to them, and prompts whose text
// names it. One list for the operation browser and the connection page.

const VIA: Record<APICaller["via"], string> = {
  source: "named in its source",
  run: "called in a run",
  prompt: "named in its text",
};

/** callerHref is where a caller is opened in the portal. */
function callerHref(c: APICaller): string | undefined {
  if (c.kind === "script") return `/portal/automations/${encodeURIComponent(c.id)}`;
  if (c.kind === "prompt") return `/portal/prompts/${encodeURIComponent(c.id)}`;
  return undefined;
}

export function UsedByList({ callers, empty }: { callers: APICaller[]; empty: string }) {
  if (callers.length === 0) {
    return <p className="text-xs text-muted-foreground">{empty}</p>;
  }
  return (
    <ul className="space-y-1">
      {callers.map((c) => {
        const href = callerHref(c);
        return (
          <li key={`${c.kind}:${c.id}:${c.via}:${c.connection ?? ""}`} className="text-xs">
            <span className="text-muted-foreground">{c.kind} </span>
            {href ? (
              <a className="font-medium underline-offset-2 hover:underline" href={href}>
                {c.name}
              </a>
            ) : (
              <span className="font-medium">{c.name}</span>
            )}
            <span className="text-muted-foreground">
              {" "}
              {VIA[c.via]}
              {c.connection ? ` on ${c.connection}` : ""}
              {c.last_seen_at ? `, last ${new Date(c.last_seen_at).toLocaleDateString()}` : ""}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** OperationUsedBy is the Used by section of the administrator's operation view. */
export function OperationUsedBy({
  catalogID,
  specName,
  operationID,
}: {
  catalogID: string;
  specName: string;
  operationID: string;
}) {
  const { data, isLoading, error } = useOperationUsedBy(catalogID, specName, operationID);
  return (
    <section aria-label="Used by" className="space-y-1.5">
      <p className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Used by</p>
      {isLoading ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : error ? (
        <p className="text-xs text-destructive">{error instanceof Error ? error.message : "Could not be read."}</p>
      ) : (
        <UsedByList
          callers={data?.used_by ?? []}
          empty="Nothing on the connections that use this catalog uses this operation."
        />
      )}
    </section>
  );
}

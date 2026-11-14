import type { CallHandlerDetail, HandlerStats } from "@/api/portal/hooks/callHandlers";

// The words a tool-call handler is described in, on its script's page, its
// connection's page and Admin > Handlers alike.

/** appliesTo is who a handler applies to. */
export function appliesTo(h: Pick<CallHandlerDetail, "personas" | "users">): string {
  const who = [...(h.personas ?? []), ...(h.users ?? [])];
  return who.length > 0 ? who.join(", ") : "Everyone";
}

/** operationsText is what a handler matches on its connection. */
export function operationsText(h: Pick<CallHandlerDetail, "operations">): string {
  const ops = h.operations ?? [];
  return ops.length > 0 ? ops.join(", ") : "Every call";
}

/** conditionsText is a handler's argument conditions. */
export function conditionsText(h: Pick<CallHandlerDetail, "conditions">): string {
  return h.conditions.map((c) => `${c.in === "path" ? "" : `${c.in} `}${c.name} = ${c.equals}`).join(", ");
}

/** versionText says which version runs, who approved it, and what waits. */
export function versionText(h: Pick<CallHandlerDetail, "running_version" | "approved_by" | "waiting_version">, latest: number): string {
  const running = h.running_version ?? 0;
  if (running === 0) return `Not running: version ${latest} was never approved`;
  const by = h.approved_by ? `, approved by ${h.approved_by}` : "";
  const waiting = h.waiting_version ?? 0;
  return waiting > running ? `v${running} runs${by}; v${waiting} waits for approval` : `v${running}${by}`;
}

/** statsText is a handler's last day, the zero counts left out. */
export function statsText(s: HandlerStats | undefined, kind: string): string[] {
  if (!s || s.matched === 0) return [kind === "watch" ? "No runs" : "No calls"];
  const counts = [`${s.matched.toLocaleString()} ${kind === "watch" ? "runs" : "matched"}`];
  if (s.held) counts.push(`${s.held.toLocaleString()} held`);
  if (s.denied) counts.push(`${s.denied.toLocaleString()} denied`);
  if (s.errors) counts.push(`${s.errors.toLocaleString()} error${s.errors === 1 ? "" : "s"}`);
  if (s.queued) counts.push(`${s.queued.toLocaleString()} queued`);
  return [counts.join(" · "), `p50 ${Math.round(s.p50_ms)} ms · p99 ${Math.round(s.p99_ms)} ms`];
}

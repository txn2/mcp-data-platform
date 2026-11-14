import { useState } from "react";
import { ChevronDown, ChevronRight, ChevronUp, ShieldCheck } from "lucide-react";
import { useConnectionHandlers, useOrderTransforms, type CallHandlerDetail } from "@/api/portal/hooks/callHandlers";
import type { CallHandlerKind } from "@/api/portal/hooks/scriptHandlers";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAuthStore } from "@/stores/auth";
import { appliesTo, conditionsText, operationsText, statsText } from "@/pages/scripts/callhandlers/format";
import { AddCallHandlerDialog } from "./AddCallHandlerDialog";
import { TestCallDialog } from "./TestCallDialog";

// ConnectionHandlersCard is a connection's Handlers section (#2436, #2634,
// #2635): its policies, its transforms in the order they run, and its
// watches, each with who it applies to, what it matches, whether it is on and
// its last day's counts. An administrator's only: anyone else never sees it,
// and the routes behind it refuse them. A row opens its script's page.

const BASE = import.meta.env.BASE_URL.replace(/\/+$/, "");

const SECTIONS: { kind: CallHandlerKind; title: string; hint: string }[] = [
  { kind: "policy", title: "Policies", hint: "decide whether a call runs: deny beats approval beats allow" },
  { kind: "transform", title: "Transforms", hint: "change what the caller gets, in this order" },
  { kind: "watch", title: "Watches", hint: "run a script after a matched call succeeds" },
];

export function ConnectionHandlersCard({ connectionName }: { connectionName: string }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const { data, isLoading, error } = useConnectionHandlers(connectionName, isAdmin);
  const [adding, setAdding] = useState(false);
  const [testing, setTesting] = useState(false);
  if (!isAdmin) return null;
  return (
    <SectionCard
      title="Handlers"
      action={
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => setTesting(true)}>
            Test a call
          </Button>
          <Button size="sm" onClick={() => setAdding(true)}>
            Add handler
          </Button>
        </div>
      }
      data-testid="connection-handlers"
    >
      <p className="mb-3 text-sm text-muted-foreground">
        Scripts that decide whether a call on this connection runs, change what it returns, or run after it. Each runs
        with the roles of the administrator who approved its version.
      </p>
      {isLoading && (
        <div className="space-y-3 py-1" aria-label="Loading the handlers">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-4 w-full" />
          ))}
        </div>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>The handlers could not be read.</AlertDescription>
        </Alert>
      )}
      {data && data.length === 0 && (
        <EmptyState icon={ShieldCheck}>
          No handlers on this connection. Every call its personas may make runs as they allow. A policy can hold or
          refuse some of those calls, a transform can change what they return, and a watch can run a script after them.
        </EmptyState>
      )}
      {data && data.length > 0 && (
        <div className="space-y-5">
          {SECTIONS.map((s) => (
            <KindSection key={s.kind} connection={connectionName} section={s} handlers={data.filter((h) => h.kind === s.kind)} />
          ))}
          <p className="text-xs text-muted-foreground">
            A row opens the handler on its script&apos;s page. Counts and times come from the audit record of the last 24
            hours; held, denied and errors show only when not zero.
          </p>
        </div>
      )}
      {adding && <AddCallHandlerDialog connection={connectionName} onClose={() => setAdding(false)} />}
      {testing && <TestCallDialog connection={connectionName} onClose={() => setTesting(false)} />}
    </SectionCard>
  );
}

function KindSection({
  connection,
  section,
  handlers,
}: {
  connection: string;
  section: (typeof SECTIONS)[number];
  handlers: CallHandlerDetail[];
}) {
  const order = useOrderTransforms(connection);
  if (handlers.length === 0) return null;
  const move = (i: number, by: number) => {
    const ids = handlers.map((h) => h.handler);
    const j = i + by;
    [ids[i], ids[j]] = [ids[j] as string, ids[i] as string];
    order.mutate(ids);
  };
  return (
    <div className="space-y-1.5" data-testid={`handlers-${section.kind}`}>
      <div className="flex flex-wrap items-baseline gap-2">
        <h3 className="text-sm font-semibold">{section.title}</h3>
        <span className="text-xs text-muted-foreground">{section.hint}</span>
      </div>
      <div className="max-w-full overflow-x-auto rounded-md border">
        <table className="w-full min-w-[720px] text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr className="border-b">
              {section.kind === "transform" && <th className="p-2 text-left font-medium">Order</th>}
              <th className="p-2 text-left font-medium">Script</th>
              <th className="p-2 text-left font-medium">Applies to</th>
              <th className="p-2 text-left font-medium">{section.kind === "watch" ? "Operations and condition" : "Operations"}</th>
              {section.kind === "transform" && <th className="p-2 text-left font-medium">Protects</th>}
              <th className="p-2 text-left font-medium">State</th>
              <th className="p-2 text-left font-medium">Last 24 hours</th>
              <th className="w-6 p-2" />
            </tr>
          </thead>
          <tbody>
            {handlers.map((h, i) => (
              <HandlerRow
                key={h.handler}
                handler={h}
                order={
                  section.kind === "transform"
                    ? { place: i + 1, up: i > 0 ? () => move(i, -1) : undefined, down: i < handlers.length - 1 ? () => move(i, 1) : undefined }
                    : undefined
                }
              />
            ))}
          </tbody>
        </table>
      </div>
      {order.error && (
        <Alert variant="destructive">
          <AlertDescription>{order.error.message}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}

function HandlerRow({
  handler: h,
  order,
}: {
  handler: CallHandlerDetail;
  order?: { place: number; up?: () => void; down?: () => void };
}) {
  const href = `${BASE}/automations/${h.script_id}`;
  const stats = statsText(h.stats, h.kind);
  return (
    <tr className="border-b align-top last:border-b-0 hover:bg-muted/40" data-testid="connection-handler-row" data-handler={h.handler}>
      {order && <OrderCell name={h.name} order={order} />}
      <td className="p-2">
        <a href={href} className="block min-w-0 font-medium hover:underline">
          {h.name}
        </a>
        <VersionLine handler={h} />
      </td>
      <td className="p-2">
        <span className="break-words">{appliesTo(h)}</span>
      </td>
      <MatchCell handler={h} />
      {h.kind === "transform" && <ProtectsCell protects={h.protected ?? []} />}
      <td className="p-2">
        <Badge variant={h.enabled ? "success" : "muted"}>{h.enabled ? "On" : "Off"}</Badge>
      </td>
      <td className="p-2 text-xs text-muted-foreground">
        {stats.map((line) => (
          <p key={line} className={line.includes("error") ? "text-destructive" : undefined}>
            {line}
          </p>
        ))}
      </td>
      <td className="p-2">
        <a href={href} aria-label={`Open ${h.name}`} className="text-muted-foreground">
          <ChevronRight aria-hidden className="size-4" />
        </a>
      </td>
    </tr>
  );
}

function OrderCell({ name, order }: { name: string; order: { place: number; up?: () => void; down?: () => void } }) {
  return (
    <td className="p-2">
      <div className="flex items-center gap-1">
        <span className="w-4">{order.place}</span>
        {order.up && (
          <Button variant="outline" size="icon" className="size-7" aria-label={`Move ${name} up`} onClick={order.up}>
            <ChevronUp aria-hidden className="size-3.5" />
          </Button>
        )}
        {order.down && (
          <Button variant="outline" size="icon" className="size-7" aria-label={`Move ${name} down`} onClick={order.down}>
            <ChevronDown aria-hidden className="size-3.5" />
          </Button>
        )}
      </div>
    </td>
  );
}

function MatchCell({ handler: h }: { handler: CallHandlerDetail }) {
  return (
    <td className="p-2">
      <span className="break-words font-mono text-xs">{operationsText(h)}</span>
      {h.conditions.length > 0 && <p className="break-words font-mono text-xs text-muted-foreground">where {conditionsText(h)}</p>}
      {h.kind === "policy" && h.items_path && <p className="text-xs text-muted-foreground">items: body.{h.items_path}</p>}
    </td>
  );
}

function ProtectsCell({ protects }: { protects: string[] }) {
  return (
    <td className="p-2">
      <span className="break-words font-mono text-xs">{protects.length > 0 ? protects.join(", ") : "Nothing"}</span>
    </td>
  );
}

// VersionLine says which version runs and who approved it, or that a newer
// one waits, or that nothing runs yet.
function VersionLine({ handler: h }: { handler: CallHandlerDetail }) {
  const running = h.running_version ?? 0;
  const waiting = h.waiting_version ?? 0;
  if (running === 0) {
    return <p className="text-xs text-amber-700 dark:text-amber-300">Not running: no version is approved</p>;
  }
  if (waiting > running) {
    return (
      <p className="text-xs">
        <span className="text-muted-foreground">v{running} runs · </span>
        <span className="text-amber-700 dark:text-amber-300">v{waiting} waits for approval</span>
      </p>
    );
  }
  return (
    <p className="text-xs text-muted-foreground">
      v{running}
      {h.approved_by ? `, approved by ${h.approved_by}` : ""}
    </p>
  );
}

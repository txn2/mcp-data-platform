import { useState } from "react";
import { ChevronRight, Plus, Zap } from "lucide-react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import {
  HANDLER_TYPE_LABELS,
  endpointOf,
  isCallHandlerKind,
  scheduleOf,
  useDeleteHandler,
  useSaveHandler,
  useScriptHandlers,
  type HandlerDetail,
  type HandlerKind,
  type HandlerOffer,
  type ScriptHandlers as Handlers,
} from "@/api/portal/hooks/scriptHandlers";
import type { CallHandlerDetail } from "@/api/portal/hooks/callHandlers";
import { useAuthStore } from "@/stores/auth";
import { CallHandlerForm } from "./callhandlers/CallHandlerForm";
import { CallHandlerPanel } from "./callhandlers/CallHandlerPanel";
import { operationsText } from "./callhandlers/format";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { EndpointEditor, EndpointHandler, SaveWarning } from "./EndpointHandler";
import { FormPublish } from "./endpoints/FormPublish";
import { HandlerTypeBadge } from "./HandlerTypeBadge";
import { ScheduleHandler, scheduleHeadline } from "./ScheduleHandler";
import { AddWebhook, WebhookHandler } from "./WebhookHandler";

// ScriptHandlers is the Handlers section of a script's page (#2435): what
// starts the script. Each handler is a row with its type, what it answers to
// and whether it is on; opening a row shows that kind's own controls. Add
// handler offers the kinds the viewer may add, and says why for the ones they
// may not. A script may hold several handlers of each kind.

const KIND_LABELS: Record<HandlerKind, string> = {
  endpoint: "Endpoint",
  schedule: "Schedule",
  webhook: "Webhook",
  policy: "Policy",
  transform: "Transform",
  watch: "Watch",
};

export function ScriptHandlers({ scriptId, contract }: { scriptId: string; contract: ScriptContract }) {
  const { data, isLoading, error } = useScriptHandlers(scriptId, true);
  const [adding, setAdding] = useState<HandlerKind | null>(null);
  // warning is what the last addition's save said beside its outcome (#2556),
  // shown once the form that made it has closed, until the next addition.
  const [warning, setWarning] = useState("");
  const pick = (kind: HandlerKind) => {
    setWarning("");
    setAdding(kind);
  };
  return (
    <SectionCard
      title="Handlers"
      action={
        data && (
          <AddHandlerMenu offers={data.add} onPick={pick} disabled={adding !== null} isForm={Boolean(data.endpoint?.is_form)} />
        )
      }
      data-testid="script-handlers"
    >
      <div className="space-y-3">
        {isLoading && <p className="text-sm text-muted-foreground">Loading the handlers...</p>}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>This script&apos;s handlers could not be read.</AlertDescription>
          </Alert>
        )}
        <SaveWarning warning={warning} />
        {data && <HandlerVersionBanner scriptId={scriptId} contract={contract} handlers={data.handlers} />}
        {data && adding && (
          <AddHandler
            key={adding}
            kind={adding}
            scriptId={scriptId}
            contract={contract}
            data={data}
            onDone={(saved) => {
              setWarning(saved?.warning ?? "");
              setAdding(null);
            }}
          />
        )}
        {data && <HandlerList scriptId={scriptId} contract={contract} handlers={data.handlers} adding={adding !== null} />}
      </div>
    </SectionCard>
  );
}

// HandlerList is the script's handlers, one row each, or the sentence saying
// nothing starts it, which an addition in progress replaces.
function HandlerList({
  scriptId,
  contract,
  handlers,
  adding,
}: {
  scriptId: string;
  contract: ScriptContract;
  handlers: HandlerDetail[];
  adding: boolean;
}) {
  if (handlers.length === 0) {
    if (adding) return null;
    return (
      <EmptyState icon={Zap}>
        Nothing starts this script but someone running it. Add a handler to run it on a schedule, answer an endpoint,
        or run for each event a webhook source stores.
      </EmptyState>
    );
  }
  return (
    <ul className="divide-y rounded-md border">
      {handlers.map((h) => (
        <HandlerItem key={h.handler} scriptId={scriptId} contract={contract} handler={h} />
      ))}
    </ul>
  );
}

// AddHandlerMenu offers each kind; one the viewer may not add is shown
// disabled with the reason.
function AddHandlerMenu({
  offers,
  onPick,
  disabled,
  isForm,
}: {
  offers: HandlerOffer[];
  onPick: (kind: HandlerKind) => void;
  disabled: boolean;
  isForm: boolean;
}) {
  if (!offers.some((o) => o.allowed)) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" variant="outline" disabled={disabled}>
          <Plus aria-hidden className="size-3.5" />
          Add handler
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        {offers.map((o) => (
          <DropdownMenuItem key={o.kind} disabled={!o.allowed} onSelect={() => onPick(o.kind)}>
            <div className="flex flex-col gap-0.5">
              <span className="font-medium">{KIND_LABELS[o.kind]}</span>
              <span className="text-xs text-muted-foreground">{o.allowed ? kindHint(o.kind, isForm) : o.reason}</span>
            </div>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function kindHint(kind: HandlerKind, isForm: boolean): string {
  switch (kind) {
    case "endpoint":
      return isForm ? "Publish the form at /forms/<name>" : "Answer HTTP requests at /endpoints/<name>";
    case "schedule":
      return "Run on a cadence";
    case "webhook":
      return "Run for each event a webhook source stores";
    case "policy":
      return "Decide whether calls on a connection run";
    case "transform":
      return "Change what calls on a connection return";
    case "watch":
      return "Run after calls on a connection succeed";
  }
}

// AddHandler is the form for the kind being added.
function AddHandler({
  kind,
  scriptId,
  contract,
  data,
  onDone,
}: {
  kind: HandlerKind;
  scriptId: string;
  contract: ScriptContract;
  data: Handlers;
  /** onDone is told the addition ended: what the save answered, or nothing for a cancel. */
  onDone: (saved?: { warning?: string }) => void;
}) {
  const endpoint = newEndpoint(contract, data);
  return (
    <div className="space-y-3 rounded-md border bg-muted/30 p-3" data-testid={`add-${kind}`}>
      <p className="text-sm font-medium">Add {KIND_LABELS[kind].toLowerCase()}</p>
      {/* A form's owner previews it and asks for public access before it is
          published (#2494), so both are offered where its endpoint is added. */}
      {kind === "endpoint" && endpoint.is_form && <FormPublish scriptId={scriptId} />}
      {kind === "endpoint" && <EndpointEditor scriptId={scriptId} endpoint={endpoint} onDone={onDone} />}
      {kind === "schedule" && (
        <ScheduleHandler scriptId={scriptId} contract={contract} schedule={null} onDone={() => onDone()} />
      )}
      {kind === "webhook" && (
        <AddWebhook
          scriptId={scriptId}
          sources={data.sources}
          held={data.handlers.filter((h) => h.kind === "webhook").map((h) => String(h.event_source ?? ""))}
          onDone={() => onDone()}
        />
      )}
      {isCallHandlerKind(kind) && (
        <CallHandlerForm
          scriptId={scriptId}
          kind={kind}
          approval={{ latest: contract.version, running: runningVersion(data.handlers) }}
          onDone={() => onDone()}
        />
      )}
    </div>
  );
}

// newEndpoint is an endpoint not added yet, carrying what adding one offers:
// where a page would answer and, on a form, the connections its submissions
// could be stored on.
function newEndpoint(contract: ScriptContract, data: Handlers): ScriptEndpoint {
  return { ...(data.endpoint ?? {}), name: contract.name, bound: false };
}

// HandlerItem is one handler: its type, what it answers to and whether it is
// on, opening onto its kind's controls and its delete.
function HandlerItem({
  scriptId,
  contract,
  handler,
}: {
  scriptId: string;
  contract: ScriptContract;
  handler: HandlerDetail;
}) {
  const [open, setOpen] = useState(false);
  return (
    <li data-testid="handler-row" data-handler={handler.handler}>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex w-full min-w-0 flex-wrap items-center gap-2 px-3 py-2 text-left text-sm hover:bg-muted/50"
      >
        <ChevronRight
          aria-hidden
          className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
        />
        <HandlerTypeBadge type={handler.type} />
        <span className="min-w-0 flex-1 truncate">{handlerTarget(handler)}</span>
        <Badge variant={handler.enabled ? "success" : "muted"}>{handler.enabled ? "On" : "Off"}</Badge>
      </button>
      {open && (
        <div className="space-y-4 border-t px-3 py-3">
          <HandlerPanel scriptId={scriptId} contract={contract} handler={handler} />
          <DeleteHandler handler={handler} />
        </div>
      )}
    </li>
  );
}

// handlerTarget is what a handler answers to, in words.
export function handlerTarget(h: HandlerDetail): string {
  if (isCallHandlerKind(h.kind)) return `${String(h.connection ?? "")} · ${operationsText(h as CallHandlerDetail)}`;
  switch (h.kind) {
    case "endpoint":
      return `/endpoints/${String(h.endpoint ?? "")}${String(h.path ?? "")}`;
    case "schedule":
      return scheduleHeadline(scheduleOf(h)).replace(/^(Runs|Paused): /, "");
    case "webhook":
      return `Each event of ${String(h.event_source ?? "")}`;
  }
  return "";
}

function HandlerPanel({
  scriptId,
  contract,
  handler,
}: {
  scriptId: string;
  contract: ScriptContract;
  handler: HandlerDetail;
}) {
  switch (handler.kind) {
    case "endpoint":
      return <EndpointHandler scriptId={scriptId} endpoint={endpointOf(handler)} />;
    case "schedule":
      return <ScheduleHandler scriptId={scriptId} contract={contract} schedule={scheduleOf(handler)} />;
    case "webhook":
      return <WebhookHandler scriptId={scriptId} handler={handler} />;
    case "policy":
    case "transform":
    case "watch":
      return <CallHandlerPanel handler={handler as CallHandlerDetail} contract={contract} />;
  }
}

// DeleteHandler deletes the handler after a confirmation that says what goes.
function DeleteHandler({ handler }: { handler: HandlerDetail }) {
  const remove = useDeleteHandler(handler.handler);
  const [confirming, setConfirming] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const label = HANDLER_TYPE_LABELS[handler.type].toLowerCase();
  const submit = async () => {
    setFailure(null);
    try {
      await remove.mutateAsync();
      setConfirming(false);
    } catch (e: unknown) {
      setFailure(e instanceof Error ? e.message : `The ${label} could not be deleted`);
    }
  };
  return (
    <div className="flex justify-end">
      <Button size="sm" variant="ghost" className="text-destructive" onClick={() => setConfirming(true)}>
        Delete {label}
      </Button>
      <ConfirmDialog
        open={confirming}
        onOpenChange={(open) => {
          setConfirming(open);
          if (!open) setFailure(null);
        }}
        title={`Delete this ${label}?`}
        description={whatGoes(handler)}
        confirmLabel={`Delete ${label}`}
        destructive
        loading={remove.isPending}
        error={failure}
        onConfirm={submit}
      />
    </div>
  );
}

// CALL_WHAT_GOES is what deleting a tool-call handler of each kind ends.
const CALL_WHAT_GOES: Record<string, string> = {
  policy: "are no longer decided by this script. Its failures go with it.",
  transform: "return what the tool answers, unchanged by this script. Its failures go with it.",
  watch: "stop starting runs of this script. The runs they already started stay.",
};

function whatGoes(h: HandlerDetail): string {
  const call = CALL_WHAT_GOES[h.kind];
  if (call) return `Calls on ${String(h.connection ?? "")} ${call}`;
  switch (h.kind) {
    case "endpoint":
      return `/endpoints/${String(h.endpoint ?? "")} answers nothing from then on, and its failed requests go with it. A form's submissions stay stored.`;
    case "schedule":
      return "The schedule stops firing. A schedule that already produced runs is kept, because its runs name it: pause it instead.";
    case "webhook":
      return `Events of ${String(h.event_source ?? "")} stop starting runs of this script. The runs they already started stay.`;
  }
  return "";
}

// runningVersion is the version a script's tool-call handlers run, 0 when
// none is approved.
function runningVersion(handlers: HandlerDetail[]): number {
  for (const h of handlers) {
    if (isCallHandlerKind(h.kind)) return (h as CallHandlerDetail).running_version ?? 0;
  }
  return 0;
}

// HandlerVersionBanner says when a newer version of a script that decides,
// changes or follows other people's calls waits for an administrator (#2436):
// the approved version keeps running meanwhile. An administrator approves it
// here; its owner is told it waits.
function HandlerVersionBanner({
  scriptId,
  contract,
  handlers,
}: {
  scriptId: string;
  contract: ScriptContract;
  handlers: HandlerDetail[];
}) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const first = handlers.find((h) => isCallHandlerKind(h.kind)) as CallHandlerDetail | undefined;
  const approve = useSaveHandler(scriptId, first?.handler);
  if (!first) return null;
  const running = first.running_version ?? 0;
  const waiting = first.waiting_version ?? 0;
  const latest = Math.max(waiting, contract.version);
  if (latest <= running) {
    return isAdmin ? null : (
      <p className="text-xs text-muted-foreground" data-testid="handler-runs-note">
        This script decides, changes or follows other people&apos;s calls. What those calls carried, and what the script
        printed for them, is visible to administrators only.
      </p>
    );
  }
  const runs = running > 0 ? `Version ${running} keeps running.` : "Nothing runs until one is approved.";
  return (
    <Alert data-testid="handler-version-banner">
      <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
        <span>
          {isAdmin
            ? `Version ${latest} waits for approval for handlers. ${runs}`
            : `Version ${latest} waits for an administrator. ${runs}`}
        </span>
        {isAdmin && (
          <Button size="sm" disabled={approve.isPending} onClick={() => approve.mutate({ approve: true })}>
            Approve version {latest}
          </Button>
        )}
      </AlertDescription>
    </Alert>
  );
}

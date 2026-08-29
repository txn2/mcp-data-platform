import { FileCode2 } from "lucide-react";
import { useScriptContract } from "@/api/portal/hooks/scripts";
import type { ScriptContract, ScriptParam } from "@/api/portal/hooks/scripts";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useAuthStore } from "@/stores/auth";
import { scheduleLine } from "./cadence";
import { executionState, formatWhen } from "./runFormat";
import { ScriptDocumentation } from "./ScriptDocumentation";
import { ScriptOwnerTransfer } from "./ScriptOwnerTransfer";
import { ScriptRunHistory } from "./ScriptRunHistory";
import { ScriptScheduleEditor } from "./ScriptScheduleEditor";
import { ScriptSourceEditor } from "./ScriptSourceEditor";
import { ScriptStateCard } from "./ScriptStateCard";

// ScriptDetailPage is one script in full: what it is and what it takes, what
// will execute it, on what schedule, and — for its owner — everything it has
// run (#1290), plus what the owner does here. They change the code, and the
// saved version is the version that runs (#1307), after checking it against
// the interpreter and running it once as themselves (#1364); they run it now
// (#1363), which is the same run the schedule produces; and they change the
// schedule, which carries no authority at all.
//
// It is ONE page for both surfaces: an administrator reads and does everything
// an owner does, on every script. Two pages would have meant two answers to
// "what can I do with this script", and the answer differing by which menu
// somebody came in through is the defect this avoids.
//
// The sections are ordered for the person debugging a script (#1406): the
// summary facts, when it fires, what it says about itself, the code, and then
// what the code has actually been doing. Source and run history are adjacent
// because they are read together — an error in the history is answered by the
// text above it.

interface Props {
  scriptId: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
  /** backLabel names where onBack goes, which differs between the two sections. */
  backLabel?: string;
  /** openRunId is one run of this script named by the address, opened in the
   * history without a click (#1405). */
  openRunId?: string;
}

export function ScriptDetailPage({
  scriptId,
  onBack,
  onNavigate,
  backLabel = "Scripts",
  openRunId,
}: Props) {
  const { data, isLoading, error } = useScriptContract(scriptId);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading script...</p>;
  }
  if (error || !data) {
    return <UnreadableScript backLabel={backLabel} onBack={onBack} />;
  }
  return (
    <ScriptDetail
      scriptId={scriptId}
      data={data}
      onBack={onBack}
      onNavigate={onNavigate}
      backLabel={backLabel}
      openRunId={openRunId}
    />
  );
}

// ScriptDetail is the page once the script is in hand, so the states that have
// no script — loading, and one this reader cannot have — are answered above it
// rather than threaded through everything below.
function ScriptDetail({
  scriptId,
  data,
  onBack,
  onNavigate,
  backLabel,
  openRunId,
}: {
  scriptId: string;
  data: { contract: ScriptContract; owned: boolean; source?: string; draft_params?: ScriptParam[] };
  onBack: () => void;
  onNavigate: (path: string) => void;
  backLabel: string;
  openRunId?: string;
}) {
  const { contract, owned, source } = data;
  const state = executionState(contract);
  // Moving a script to another person is an administrator's, and the only
  // control on this page that is not the owner's own (#1404).
  const isAdmin = useAuthStore((s) => s.isAdmin());

  return (
    <div className="space-y-4">
      <PageHeader
        backLabel={backLabel}
        onBack={onBack}
        icon={FileCode2}
        title={contract.display_name || contract.name}
        urn={contract.name}
        actions={<Badge variant={state.variant}>{state.label}</Badge>}
      />

      {state.detail && (
        <Alert>
          <AlertDescription>{state.detail}</AlertDescription>
        </Alert>
      )}

      <SectionCard title="Details">
        <ScriptFacts contract={contract} />
      </SectionCard>

      {owned && <ScriptScheduleEditor scriptId={scriptId} contract={contract} />}

      <ScriptDocumentation scriptId={scriptId} contract={contract} owned={owned} />

      {owned && (
        <>
          {/* Keyed on the script for the same reason the schedule editor is:
              this component sits at the same position in the tree for every
              script, so an address change from one script to another would
              otherwise carry a part-typed edit — and the values a real run
              binds — onto the next one. */}
          <ScriptSourceEditor
            key={scriptId}
            scriptId={scriptId}
            contract={contract}
            source={source ?? ""}
            draftParams={draftParamsOf(data)}
          />
          <ScriptRunHistory
            scriptId={scriptId}
            openRunId={openRunId}
            onNavigate={onNavigate}
          />
          {/* The state the runs above carry between them (#1537), read after
              the history because a watermark is explained by the run that
              wrote it. Keyed on the script for the reason the editors are. */}
          <ScriptStateCard key={`state-${scriptId}`} scriptId={scriptId} contract={contract} />
        </>
      )}

      {isAdmin && <ScriptOwnerTransfer scriptId={scriptId} contract={contract} />}
    </div>
  );
}

// UnreadableScript is the page for a script this reader cannot have: deleted,
// or not theirs. The two are deliberately one answer, because distinguishing
// them would tell a reader that a script they may not see exists.
function UnreadableScript({ backLabel, onBack }: { backLabel: string; onBack: () => void }) {
  return (
    <div className="space-y-4">
      <PageHeader backLabel={backLabel} onBack={onBack} icon={FileCode2} title="Script" />
      <Alert variant="destructive">
        <AlertDescription>
          This script could not be loaded. It may have been deleted, or it may not be yours
          to see.
        </AlertDescription>
      </Alert>
    </div>
  );
}

// draftParamsOf is the contract a dry run binds against: the live record's,
// which the detail route serves to the owner beside the source. It falls back
// to the contract's own parameters for a deployment that predates the field.
function draftParamsOf(data: {
  contract: ScriptContract;
  draft_params?: ScriptParam[];
}): ScriptParam[] {
  return data.draft_params ?? data.contract.params ?? [];
}

// ScriptFacts is the summary the whole page rests on: who owns it, which
// version runs, when it next fires, and what a run binds. Ownership is the
// whole of who may see it, so there is no second visibility line to read
// (#1404).
//
// The parameters used to be a section of their own, one card below these five
// lines. They are the same kind of statement — what this script is and what it
// takes — so they are read here rather than found separately (#1406).
function ScriptFacts({ contract }: { contract: ScriptContract }) {
  // In words, as every other surface states a cadence (#1405, #1407): the
  // expression is read and written in the schedule editor below, and a reader
  // asking what this script does is not asking what its cron field says.
  const schedule = contract.schedule
    ? `${scheduleLine(contract.schedule.cron_spec, contract.schedule.timezone)}${contract.schedule.enabled ? "" : " — paused"}`
    : "on demand";
  return (
    <div className="space-y-4">
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
        <Fact label="Owner" value={contract.owner_email || "nobody"} />
        <Fact label="Runs" value={`v${contract.version}, the latest saved version`} />
        <Fact label="Schedule" value={schedule} />
        <Fact
          label="Next run"
          value={contract.schedule?.enabled ? formatWhen(contract.schedule.next_run_at) : "—"}
        />
        <Fact
          label="Status"
          value={contract.enabled ? contract.status : `${contract.status} (disabled)`}
        />
        {contract.state && <Fact label="State" value={stateFact(contract.state)} />}
      </dl>
      <div className="space-y-2">
        <p className="text-xs text-muted-foreground">Parameters</p>
        <ParameterTable contract={contract} />
      </div>
    </div>
  );
}

// stateFact is what the script does with the state it carries between runs
// (#1537), in one line: read from the source, with the revision the platform
// holds. A script that keeps none says so, whatever an old revision says.
function stateFact(state: NonNullable<ScriptContract["state"]>): string {
  const keeps = state.reads_state || state.saves_state;
  if (!keeps) return "keeps none";
  const does = state.reads_state && state.saves_state
    ? "carried between runs"
    : state.saves_state
      ? "saved, never read"
      : "read, never saved";
  return state.revision === 0 ? `${does}, nothing saved yet` : `${does}, revision ${state.revision}`;
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate">{value}</dd>
    </div>
  );
}

// ParameterTable is the contract a run binds against: the live record's
// parameters, which are the latest saved version's.
function ParameterTable({ contract }: { contract: ScriptContract }) {
  const params = contract.params ?? [];
  if (params.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        This script takes no parameters; every run of it computes the same thing.
      </p>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>Required</TableHead>
          <TableHead>Description</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {params.map((p) => (
          <TableRow key={p.name}>
            <TableCell className="font-mono text-xs">{p.name}</TableCell>
            <TableCell className="text-xs">{p.type}</TableCell>
            <TableCell className="text-xs">
              {p.required ? "required" : `optional${p.default === undefined ? "" : ` (${String(p.default)})`}`}
            </TableCell>
            <TableCell className="text-xs text-muted-foreground">{p.description || "—"}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

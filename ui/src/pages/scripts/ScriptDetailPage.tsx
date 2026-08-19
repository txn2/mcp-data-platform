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
import { executionState, formatWhen } from "./runFormat";
import { ScriptRunHistory } from "./ScriptRunHistory";
import { ScriptRunPanel } from "./ScriptRunPanel";
import { ScriptScheduleEditor } from "./ScriptScheduleEditor";
import { ScriptSourceEditor } from "./ScriptSourceEditor";
import { ScriptVersionHistory } from "./ScriptVersionHistory";

// ScriptDetailPage is one script in full: what it is and what it takes, what
// will execute it, on what cadence, and — for its owner — everything it has run
// (#1290), plus what the owner does here. They run it now (#1363), which is the
// same run the schedule produces; they change the code, which an edit sends
// back through review (#1307), after checking it against the interpreter and
// running it once as themselves (#1364); and they change the cadence, which
// carries no authority at all.
//
// The top of the page is the contract document, the same one a reference to
// this script resolves to for an agent. There is deliberately not a second
// answer to "what is this script": a field a reader needs belongs in the
// contract rather than beside it.

interface Props {
  scriptId: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
}

export function ScriptDetailPage({ scriptId, onBack, onNavigate }: Props) {
  const { data, isLoading, error } = useScriptContract(scriptId);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading script...</p>;
  }
  if (error || !data) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel="Scripts" onBack={onBack} icon={FileCode2} title="Script" />
        <Alert variant="destructive">
          <AlertDescription>
            This script could not be loaded. It may have been deleted, or it may not be
            yours to see.
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  const { contract, owned, source } = data;
  const state = executionState(contract);

  return (
    <div className="space-y-4">
      <PageHeader
        backLabel="Scripts"
        onBack={onBack}
        icon={FileCode2}
        title={contract.display_name || contract.name}
        urn={contract.name}
        subtitle={contract.description}
        actions={<Badge variant={state.variant}>{state.label}</Badge>}
      />

      {state.detail && (
        <Alert>
          <AlertDescription>{state.detail}</AlertDescription>
        </Alert>
      )}

      <SectionCard title="Contract">
        <ContractFacts contract={contract} />
      </SectionCard>

      <SectionCard title="Parameters">
        <ParameterTable contract={contract} />
      </SectionCard>

      {owned ? (
        <OwnerSections
          scriptId={scriptId}
          contract={contract}
          source={source ?? ""}
          draftParams={draftParamsOf(data)}
          onNavigate={onNavigate}
        />
      ) : (
        <p className="text-xs text-muted-foreground">
          This script belongs to {contract.owner_email || "someone else"}. Its cadence, its
          source, its capability grant, and its run history are theirs and the administrators'
          to read.
        </p>
      )}
    </div>
  );
}

// draftParamsOf is the contract a dry run binds against: the LIVE record's,
// which the detail route serves to the owner beside the source. It falls back
// to the contract's own parameters for a deployment that predates the field —
// the two agree except on a script whose approved version and live record carry
// different parameter contracts, which is what the field exists for.
function draftParamsOf(data: {
  contract: ScriptContract;
  draft_params?: ScriptParam[];
}): ScriptParam[] {
  return data.draft_params ?? data.contract.params ?? [];
}

// OwnerSections is everything the contract does not say out loud: the run the
// owner asks for now, the cadence they set, the code behind the execution gate,
// and what the automation has actually been doing. All four are the owner's and
// the administrators', and they appear or are absent together.
//
// Running comes first because it is what somebody opening their own automation
// most often came to do; the code and the cadence are what they change when it
// is not doing what they wanted.
function OwnerSections({
  scriptId,
  contract,
  source,
  draftParams,
  onNavigate,
}: {
  scriptId: string;
  contract: ScriptContract;
  source: string;
  /** The live record's parameter contract, which is what a dry run binds. */
  draftParams: ScriptParam[];
  onNavigate: (path: string) => void;
}) {
  return (
    <>
      <ScriptRunPanel scriptId={scriptId} contract={contract} />
      <ScriptSourceEditor
        scriptId={scriptId}
        contract={contract}
        source={source}
        draftParams={draftParams}
      />
      <ScriptScheduleEditor scriptId={scriptId} contract={contract} />
      <ScriptVersionHistory scriptId={scriptId} contract={contract} />
      <ScriptRunHistory scriptId={scriptId} onNavigate={onNavigate} />
    </>
  );
}

// ContractFacts is the summary every surface agrees on: who owns it, who may
// see it, what approved it, and when it next fires.
function ContractFacts({ contract }: { contract: ScriptContract }) {
  const approval = contract.approval.approved
    ? `v${contract.approval.version} by ${contract.approval.approved_by || "unknown"} on ${formatWhen(contract.approval.approved_at)}`
    : "nothing approved";
  const cadence = contract.schedule
    ? `${contract.schedule.cron_spec} (${contract.schedule.timezone})${contract.schedule.enabled ? "" : " — paused"}`
    : "on demand";
  return (
    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
      <Fact label="Owner" value={contract.owner_email || "—"} />
      <Fact label="Visible to" value={visibility(contract)} />
      <Fact label="Approved" value={approval} />
      <Fact label="Schedule" value={cadence} />
      <Fact
        label="Next run"
        value={contract.schedule?.enabled ? formatWhen(contract.schedule.next_run_at) : "—"}
      />
      <Fact label="Status" value={contract.enabled ? contract.status : `${contract.status} (disabled)`} />
    </dl>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate">{value}</dd>
    </div>
  );
}

// visibility renders the script's scope in the reader's terms rather than the
// store's: "personal" means its owner, and a persona-scoped script names the
// personas it serves.
function visibility(contract: ScriptContract): string {
  if (contract.scope === "global") return "everyone";
  if (contract.scope === "persona") {
    return (contract.personas ?? []).join(", ") || "no persona";
  }
  return contract.owner_email || "its owner";
}

// ParameterTable is the contract a run binds against: the approved version's
// parameters, because that is the version anything will execute.
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

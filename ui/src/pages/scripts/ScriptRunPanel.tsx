import { useState } from "react";
import {
  SCRIPT_RUN_AUDIENCE,
  useRunScript,
  useScriptConnections,
} from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  boundParams,
  declaresConnection,
  missingRequired,
  ScriptParameterForm,
  type Values,
} from "./ScriptParameterForm";

// ScriptRunPanel is where an owner runs their automation now (#1363).
//
// Until this existed the page showed the contract, the parameters, the
// approval and the run history, and ran nothing: an owner who wanted fresh
// output before the next scheduled fire had to leave the portal and ask an
// agent. This is that action, and it is deliberately the same action — the run
// is queued and executed by a worker under the script's own identity, exactly
// as a scheduled fire is.
//
// It cannot make a script run that the platform would refuse. Whether a run
// would be admitted is the contract's own refusal, stated at the top of the
// page, and this panel reads it rather than deciding for itself: a script with
// nothing approved says so instead of offering a control that cannot work.

interface Props {
  scriptId: string;
  contract: ScriptContract;
}

export function ScriptRunPanel({ scriptId, contract }: Props) {
  // The refusal is the execution gate's own, carried on the contract. Reading
  // it rather than re-deriving the rules is what keeps this page from offering
  // a run the platform would decline. The refusal itself is stated once, at the
  // top of the page, and repeating the sentence here would only make it easier
  // to end up with two of them.
  if (contract.approval.refusal) {
    return (
      <SectionCard title="Run now">
        <p className="text-sm text-muted-foreground">
          A run asked for now would be refused, for the reason stated above. This control
          appears once a version is approved; approving one is an administrator's decision.
        </p>
      </SectionCard>
    );
  }
  return <RunForm scriptId={scriptId} contract={contract} />;
}

// RunForm is the request itself. It is separate from the refusal above so that
// it always has an approved version to run: a form for a script nothing
// executes would be a control that cannot work.
function RunForm({ scriptId, contract }: Props) {
  const params = contract.params ?? [];
  const run = useRunScript(scriptId);
  // The set a connection parameter offers is the APPROVED version's grant, not
  // the caller's own connections: an approved run is confined to what its
  // approval granted, whatever the person asking for it can reach (#1361).
  const { data: connections } = useScriptConnections(
    scriptId,
    declaresConnection(params),
    SCRIPT_RUN_AUDIENCE.run,
  );
  const [values, setValues] = useState<Values>({});
  const [queued, setQueued] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const unbound = missingRequired(params, values);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setQueued(null);
    setFailure(null);
    run.mutate(boundParams(params, values), {
      onSuccess: (res) => setQueued(res.message),
      onError: (err: unknown) =>
        setFailure(err instanceof Error ? err.message : "The run could not be queued"),
    });
  };

  return (
    <SectionCard title="Run now">
      <form className="space-y-4" onSubmit={submit}>
        <p className="text-sm text-muted-foreground">
          This runs version {contract.approval.version} — the approved one — under the script's
          own identity, with the capabilities its approval granted. It is the same run the
          schedule produces, asked for now.
        </p>

        <ScriptParameterForm
          form="run"
          params={params}
          values={values}
          disabled={run.isPending}
          connections={connections?.data}
          onChange={(name, value) => setValues({ ...values, [name]: value })}
        />

        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        {queued && !failure && (
          <Alert>
            <AlertDescription>{queued}</AlertDescription>
          </Alert>
        )}

        <RunButton pending={run.isPending} unbound={unbound} />
      </form>
    </SectionCard>
  );
}

// RunButton is the action and the reason it is unavailable, which belong
// together: a disabled button with no sentence beside it is a dead end.
function RunButton({ pending, unbound }: { pending: boolean; unbound: string[] }) {
  return (
    <div className="flex items-center gap-3">
      <Button type="submit" size="sm" disabled={pending || unbound.length > 0}>
        {pending ? "Queueing..." : "Run"}
      </Button>
      {unbound.length > 0 && (
        <p className="text-xs text-muted-foreground">
          {unbound.join(", ")} {unbound.length === 1 ? "is" : "are"} required.
        </p>
      )}
    </div>
  );
}

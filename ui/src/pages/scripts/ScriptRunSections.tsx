import type { ProducedTargetKind } from "@/api/portal/hooks/producers";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptExclusiveSetting } from "./ScriptExclusiveSetting";
import { ScriptGrantsCard } from "./ScriptGrantsCard";
import { ScriptProducedPanel } from "./ScriptProducedPanel";
import { ScriptLiveRuns } from "./ScriptRunAttempts";
import { ScriptRunHistory } from "./ScriptRunHistory";
import { ScriptStateCard } from "./ScriptStateCard";

// ScriptRunSections is everything on a script's page that is about its runs,
// read by its owner and an administrator below the code: the runs still going,
// the history, what they wrote, the state they carry, and who else may start
// one. It is one component because a library (#1941), which is never run, has
// none of it, and the page leaves it out as a whole.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  openRunId?: string;
  onNavigate: (path: string) => void;
  filePath?: (kind: ProducedTargetKind, id: string) => string;
}

export function ScriptRunSections({ scriptId, contract, openRunId, onNavigate, filePath }: Props) {
  return (
    <>
      {/* Runs that have not ended, above the history (#1860): a run whose
          worker died can be older than the history's first page. */}
      <ScriptLiveRuns scriptId={scriptId} />
      <ScriptRunHistory
        scriptId={scriptId}
        openRunId={openRunId}
        onNavigate={onNavigate}
        setting={<ScriptExclusiveSetting scriptId={scriptId} contract={contract} />}
      />
      {/* Everything the runs above have written, as one list rather than
          per-run output lines (#1569). It reads after the history for the
          same reason the history reads after the source: it is the
          aggregate the individual accounts add up to. */}
      <ScriptProducedPanel
        scriptId={scriptId}
        owner={contract.owner_email}
        filePath={filePath}
        onNavigate={onNavigate}
      />
      {/* The state the runs above carry between them (#1537), read after
          the history because a watermark is explained by the run that
          wrote it. Keyed on the script for the reason the editors are. */}
      <ScriptStateCard key={`state-${scriptId}`} scriptId={scriptId} contract={contract} />
      {/* Who else may run it (#1846), the owner's to decide. */}
      <ScriptGrantsCard key={`grants-${scriptId}`} scriptId={scriptId} />
    </>
  );
}

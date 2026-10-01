import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";

import type { ScriptDryRun } from "@/api/portal/hooks/scriptDrafts";
import { DryRunReport } from "./ScriptDraftChecks";

function dryRun(over: Partial<ScriptDryRun>): ScriptDryRun {
  return {
    run_id: "dry_1",
    status: "failed",
    metrics: { steps: 1, duration_ms: 1, queries: 0, exports: 0 },
    outputs: [],
    writes: [],
    message: "",
    ...over,
  } as ScriptDryRun;
}

// A failed draft's save_state is shown as discarded, and a checkpoint as the
// state that would be kept (#2002, #2003).
describe("DryRunReport state", () => {
  it("labels a checkpoint and a discarded save_state", () => {
    render(
      <DryRunReport
        result={dryRun({
          state: { through: 2 },
          state_checkpoint: true,
          state_discarded: { through: 3 },
        })}
      />,
    );
    expect(screen.getByText(/Would save this checkpoint/)).toBeInTheDocument();
    expect(screen.getByText(/a platform run would discard this save_state/)).toBeInTheDocument();
    expect(screen.getByText(/"through": 3/)).toBeInTheDocument();
  });

  it("labels a successful draft's save_state as state", () => {
    render(<DryRunReport result={dryRun({ status: "succeeded", state: { through: 3 } })} />);
    expect(screen.getByText(/Would save this state/)).toBeInTheDocument();
    expect(screen.queryByText(/discard/)).not.toBeInTheDocument();
  });
});

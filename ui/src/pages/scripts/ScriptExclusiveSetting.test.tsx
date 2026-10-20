import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ExclusiveMode, ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptExclusiveSetting } from "./ScriptExclusiveSetting";

// The owner's choice for overlapping runs (#1986, #2209): the radios read the
// contract, send the chosen mode, and show a refusal in the server's words.

vi.mock("@/api/portal/hooks/scripts", () => ({
  useSetScriptExclusive: vi.fn(),
}));

import { useSetScriptExclusive } from "@/api/portal/hooks/scripts";

const mutate = vi.fn();

const contract = (exclusive?: ExclusiveMode): ScriptContract => ({
  id: "script-001",
  name: "nightly-sync",
  status: "active",
  enabled: true,
  params: [],
  version: 1,
  exclusive,
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useSetScriptExclusive).mockReturnValue({
    mutate,
    isPending: false,
  } as never);
});

afterEach(cleanup);

const radio = (name: RegExp) => screen.getByRole("radio", { name });

describe("ScriptExclusiveSetting", () => {
  it("states each choice and reflects the contract", () => {
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract("refuse")}
      />,
    );
    expect(
      screen.getByRole("group", { name: "Overlapping runs" }),
    ).toBeInTheDocument();
    expect(radio(/Refused/)).toBeChecked();
    expect(radio(/Refused/)).toHaveAccessibleDescription(
      "A run cannot start while another run of this script is pending or running.",
    );
    expect(radio(/Queued/)).toHaveAccessibleDescription(
      "A run started while another run of this script is running waits its turn.",
    );
  });

  it("reads a script that predates the setting as allowed", () => {
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract(undefined)}
      />,
    );
    expect(radio(/Allowed/)).toBeChecked();
  });

  it("sends the chosen mode", () => {
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract("off")}
      />,
    );
    fireEvent.click(radio(/Queued/));
    expect(mutate).toHaveBeenCalledWith("queue", expect.anything());
  });

  it("is held while a save is in flight", () => {
    vi.mocked(useSetScriptExclusive).mockReturnValue({
      mutate,
      isPending: true,
    } as never);
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract("off")}
      />,
    );
    expect(radio(/Queued/)).toBeDisabled();
  });

  it("shows the server's refusal", () => {
    mutate.mockImplementation(
      (_value: ExclusiveMode, opts: { onError: (e: unknown) => void }) =>
        opts.onError(new Error("more than one run of this script is open")),
    );
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract("off")}
      />,
    );
    fireEvent.click(radio(/Refused/));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "more than one run of this script is open",
    );
  });
});

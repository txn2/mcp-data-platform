import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptExclusiveSetting } from "./ScriptExclusiveSetting";

// The owner's choice that a script runs one at a time (#1986): the checkbox
// reads the contract, sends the new value, and shows a refusal in the
// server's words.

vi.mock("@/api/portal/hooks/scripts", () => ({
  useSetScriptExclusive: vi.fn(),
}));

import { useSetScriptExclusive } from "@/api/portal/hooks/scripts";

const mutate = vi.fn();

const contract = (exclusive?: boolean): ScriptContract => ({
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

const box = () => screen.getByRole("checkbox", { name: /One run at a time/ });

describe("ScriptExclusiveSetting", () => {
  it("states the setting and reflects the contract", () => {
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract(true)}
      />,
    );
    expect(box()).toBeChecked();
    expect(box()).toHaveAccessibleDescription(
      "A run cannot start while another run of this script is pending or running.",
    );
  });

  it("is unchecked for a script that predates the setting", () => {
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract(undefined)}
      />,
    );
    expect(box()).not.toBeChecked();
  });

  it("sends the new value", () => {
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract(false)}
      />,
    );
    fireEvent.click(box());
    expect(mutate).toHaveBeenCalledWith(true, expect.anything());
  });

  it("is held while a save is in flight", () => {
    vi.mocked(useSetScriptExclusive).mockReturnValue({
      mutate,
      isPending: true,
    } as never);
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract(false)}
      />,
    );
    expect(box()).toBeDisabled();
  });

  it("shows the server's refusal", () => {
    mutate.mockImplementation(
      (_value: boolean, opts: { onError: (e: unknown) => void }) =>
        opts.onError(new Error("more than one run of this script is open")),
    );
    render(
      <ScriptExclusiveSetting
        scriptId="script-001"
        contract={contract(false)}
      />,
    );
    fireEvent.click(box());
    expect(screen.getByRole("alert")).toHaveTextContent(
      "more than one run of this script is open",
    );
  });
});

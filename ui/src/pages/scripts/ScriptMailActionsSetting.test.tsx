import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { ScriptMailActionsSetting } from "./ScriptMailActionsSetting";

// The owner's choice whether a script may act on mail it reads (#2247).

vi.mock("@/api/portal/hooks/scriptMailActions", () => ({
  useSetScriptMailActions: vi.fn(),
}));

import { useSetScriptMailActions } from "@/api/portal/hooks/scriptMailActions";

const mutate = vi.fn();

const contract = (mailActions?: boolean): ScriptContract => ({
  id: "script-001",
  name: "vendor-mail",
  status: "active",
  enabled: true,
  params: [],
  version: 1,
  mail_actions: mailActions,
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useSetScriptMailActions).mockReturnValue({ mutate, isPending: false } as never);
});

afterEach(cleanup);

describe("ScriptMailActionsSetting", () => {
  it("reads a script that predates the setting as off, and says what off means", () => {
    render(<ScriptMailActionsSetting scriptId="script-001" contract={contract()} />);
    const box = screen.getByRole("checkbox", { name: /Reads mail and acts on it/ });
    expect(box).not.toBeChecked();
    expect(box).toHaveAccessibleDescription("A run that reads mail cannot write anything afterwards.");
  });

  it("turns it on and off", () => {
    const { rerender } = render(<ScriptMailActionsSetting scriptId="script-001" contract={contract(false)} />);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(mutate).toHaveBeenCalledWith(true, expect.anything());
    rerender(<ScriptMailActionsSetting scriptId="script-001" contract={contract(true)} />);
    expect(screen.getByRole("checkbox")).toBeChecked();
    expect(screen.getByRole("checkbox")).toHaveAccessibleDescription(/A sender can influence what it does/);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(mutate).toHaveBeenLastCalledWith(false, expect.anything());
  });

  it("shows a refusal in the server's words", () => {
    mutate.mockImplementation((_v, opts: { onError: (e: Error) => void }) => opts.onError(new Error("only the owner can change this")));
    render(<ScriptMailActionsSetting scriptId="script-001" contract={contract(false)} />);
    fireEvent.click(screen.getByRole("checkbox"));
    expect(screen.getByRole("alert")).toHaveTextContent("only the owner can change this");
  });
});

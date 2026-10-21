import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { BehaviorTab } from "./BehaviorTab";
import type { PersonaDraft } from "./types";

vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: () => <div />,
}));

const draft: PersonaDraft = {
  name: "integration",
  displayName: "Integration",
  description: "",
  roles: ["crm-sync"],
  allowTools: ["*"],
  denyTools: [],
  allowConnections: [],
  denyConnections: [],
  apiRoutes: [],
  priority: 0,
  descriptionPrefix: "",
  descriptionOverride: "",
  agentInstructionsSuffix: "",
  agentInstructionsOverride: "",
  serviceAccount: false,
  showAutomations: true,
  allowSharing: true,
};

afterEach(cleanup);

describe("BehaviorTab service account (#1980)", () => {
  it("shows the setting with what it does, and saves it into the draft", () => {
    const onUpdate = vi.fn();
    render(<BehaviorTab draft={draft} onUpdate={onUpdate} isReadOnly={false} />);
    const box = screen.getByRole("checkbox", { name: /Service account/ });
    expect(box).not.toBeChecked();
    expect(screen.getByText("Its calls are audited but not added to Calls.")).toBeInTheDocument();

    fireEvent.click(box);
    expect(onUpdate).toHaveBeenCalledWith({ serviceAccount: true });
  });

  it("reflects a marked persona and is locked when the persona cannot be edited", () => {
    render(<BehaviorTab draft={{ ...draft, serviceAccount: true }} onUpdate={vi.fn()} isReadOnly />);
    const box = screen.getByRole("checkbox", { name: /Service account/ });
    expect(box).toBeChecked();
    expect(box).toBeDisabled();
  });
});

describe("BehaviorTab Automations (#2109)", () => {
  it("shows Automations by default and saves hiding them into the draft", () => {
    const onUpdate = vi.fn();
    render(<BehaviorTab draft={draft} onUpdate={onUpdate} isReadOnly={false} />);
    const box = screen.getByRole("checkbox", { name: /^Automations/ });
    expect(box).toBeChecked();
    expect(screen.getByText("Shown in the portal. Administrators always see Automations.")).toBeInTheDocument();

    fireEvent.click(box);
    expect(onUpdate).toHaveBeenCalledWith({ showAutomations: false });
  });

  it("reflects a persona that hides them", () => {
    render(<BehaviorTab draft={{ ...draft, showAutomations: false }} onUpdate={vi.fn()} isReadOnly />);
    const box = screen.getByRole("checkbox", { name: /^Automations/ });
    expect(box).not.toBeChecked();
    expect(box).toBeDisabled();
  });
});

describe("BehaviorTab Sharing (#2241)", () => {
  it("allows sharing by default and saves withholding it into the draft", () => {
    const onUpdate = vi.fn();
    render(<BehaviorTab draft={draft} onUpdate={onUpdate} isReadOnly={false} />);
    const box = screen.getByRole("checkbox", { name: /^Sharing/ });
    expect(box).toBeChecked();
    expect(
      screen.getByText("Share what they own with a person or to a channel. Administrators always can."),
    ).toBeInTheDocument();
    fireEvent.click(box);
    expect(onUpdate).toHaveBeenCalledWith({ allowSharing: false });
  });

  it("reflects a persona denied sharing", () => {
    render(<BehaviorTab draft={{ ...draft, allowSharing: false }} onUpdate={vi.fn()} isReadOnly />);
    const box = screen.getByRole("checkbox", { name: /^Sharing/ });
    expect(box).not.toBeChecked();
    expect(box).toBeDisabled();
  });
});

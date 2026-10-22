import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import { PortalTab } from "./PortalTab";
import type { PersonaDraft } from "./types";
import { FULL_PORTAL } from "@/lib/portalAccess";

const draft: PersonaDraft = {
  name: "client",
  displayName: "Client",
  description: "",
  roles: ["client"],
  allowTools: [],
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
  portal: { ...FULL_PORTAL },
  external: false,
  allowSharing: true,
};

afterEach(cleanup);

describe("PortalTab (#2225)", () => {
  it("names every area with its level, and says administrators have every area", () => {
    render(<PortalTab draft={{ ...draft, portal: { ...FULL_PORTAL, knowledge: "read", resources: "own" } }} onUpdate={vi.fn()} isReadOnly={false} />);
    expect(screen.getByText("Administrators have every area, whatever is set here.")).toBeInTheDocument();
    expect(within(screen.getByRole("combobox", { name: "Assets and collections" })).getByText("Their own and shared")).toBeInTheDocument();
    expect(within(screen.getByRole("combobox", { name: "Resources" })).getByText("Own folder")).toBeInTheDocument();
    expect(within(screen.getByRole("combobox", { name: "Knowledge pages" })).getByText("Read")).toBeInTheDocument();
    expect(within(screen.getByRole("combobox", { name: "Catalog" })).getByText("Edit")).toBeInTheDocument();
    expect(within(screen.getByRole("combobox", { name: "APIs" })).getByText("Browse")).toBeInTheDocument();
    for (const name of [/^Feedback/, /^Prompts/, /^Automations/, /^Search/, /^Memory/, /^Download/, /^Sharing/]) {
      expect(screen.getByRole("checkbox", { name })).toBeChecked();
    }
    expect(screen.getByText("Threads, comments and sign-off")).toBeInTheDocument();
    expect(screen.getByText("Download files and export PDFs")).toBeInTheDocument();
  });

  it("saves a switch into the draft's portal block, keeping the other areas", () => {
    const onUpdate = vi.fn();
    render(<PortalTab draft={draft} onUpdate={onUpdate} isReadOnly={false} />);
    fireEvent.click(screen.getByRole("checkbox", { name: /^Automations/ }));
    expect(onUpdate).toHaveBeenCalledWith({ portal: { ...FULL_PORTAL, automations: false } });
  });

  it("saves withholding sharing into the draft", () => {
    const onUpdate = vi.fn();
    render(<PortalTab draft={draft} onUpdate={onUpdate} isReadOnly={false} />);
    fireEvent.click(screen.getByRole("checkbox", { name: /^Sharing/ }));
    expect(onUpdate).toHaveBeenCalledWith({ allowSharing: false });
  });

  it("is locked when the persona cannot be edited", () => {
    render(<PortalTab draft={{ ...draft, portal: { ...FULL_PORTAL, download: false } }} onUpdate={vi.fn()} isReadOnly />);
    const box = screen.getByRole("checkbox", { name: /^Download/ });
    expect(box).not.toBeChecked();
    expect(box).toBeDisabled();
    expect(screen.getByRole("combobox", { name: "Resources" })).toBeDisabled();
  });

  it("marks the persona external (#2092)", () => {
    const onUpdate = vi.fn();
    render(<PortalTab draft={draft} onUpdate={onUpdate} isReadOnly={false} />);
    expect(screen.getByText("Sees only what is shared with them. No tools, keys, prompts or people directory.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("checkbox", { name: /^External/ }));
    expect(onUpdate).toHaveBeenCalledWith({ external: true });
  });
});

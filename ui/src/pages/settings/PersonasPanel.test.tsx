import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { hasUnsavedChanges } from "@/lib/navigationGuard";
import type { PersonaDetail } from "@/api/admin/types";

// The panel's hooks are mocked so the persona list and detail can be put in
// front of it; the editor is a stub that shows what the panel handed it, since
// what is under test is which persona the panel opens and the draft it builds.
vi.mock("@/api/admin/hooks", () => ({
  usePersonas: vi.fn(),
  usePersonaDetail: vi.fn(),
  useDeletePersona: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useSystemInfo: vi.fn(() => ({ data: { config_mode: "database" } })),
}));

vi.mock("./PersonaEditor", () => ({
  PersonaEditor: ({
    selectedName,
    draft,
    onUpdate,
    onSave,
  }: {
    selectedName: string | null;
    draft: { serviceAccount: boolean };
    onUpdate: (partial: { displayName: string }) => void;
    onSave: () => void;
  }) => (
    <div>
      <span data-testid="editing">{selectedName}</span>
      <span data-testid="service-account">{String(draft.serviceAccount)}</span>
      <button type="button" onClick={() => onUpdate({ displayName: "Edited" })}>
        edit
      </button>
      <button type="button" onClick={onSave}>
        saved
      </button>
    </div>
  ),
}));

import { usePersonas, usePersonaDetail } from "@/api/admin/hooks";
import { PersonasPanel } from "./PersonasPanel";
import { FULL_PORTAL } from "@/lib/portalAccess";

const summaries = [
  { name: "analyst", display_name: "Analyst", roles: ["analyst"], tool_count: 3 },
  { name: "integration", display_name: "Integration", roles: ["crm-sync"], tool_count: 3 },
];

function detail(name: string, serviceAccount: boolean): PersonaDetail {
  return {
    name,
    display_name: name,
    roles: [name],
    priority: 0,
    allow_tools: ["*"],
    deny_tools: [],
    tools: [],
    api_routes: [],
    service_account: serviceAccount,
    portal: { ...FULL_PORTAL },
  };
}

// One answer per persona, the same object on every render, as the query cache
// hands out: the panel copies a detail into its draft whenever it changes.
const details: Record<string, ReturnType<typeof usePersonaDetail>> = {};
const personaList = { data: { personas: summaries, total: summaries.length }, isLoading: false };
const none = { data: undefined } as unknown as ReturnType<typeof usePersonaDetail>;

beforeEach(() => {
  for (const s of summaries) {
    details[s.name] = { data: detail(s.name, s.name === "integration") } as unknown as ReturnType<
      typeof usePersonaDetail
    >;
  }
  vi.mocked(usePersonas).mockReturnValue(personaList as unknown as ReturnType<typeof usePersonas>);
  vi.mocked(usePersonaDetail).mockImplementation((name: string | null) =>
    name && details[name] ? details[name] : none,
  );
});

afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

describe("PersonasPanel", () => {
  it("opens the persona a link names (#1980)", () => {
    window.history.replaceState(null, "", "/portal/admin/personas?persona=integration");
    render(<PersonasPanel />);
    expect(screen.getByTestId("editing")).toHaveTextContent("integration");
    expect(screen.getByTestId("service-account")).toHaveTextContent("true");
  });

  it("opens the first persona when the link names one that does not exist", () => {
    window.history.replaceState(null, "", "/portal/admin/personas?persona=gone");
    render(<PersonasPanel />);
    expect(screen.getByTestId("editing")).toHaveTextContent("analyst");
    expect(screen.getByTestId("service-account")).toHaveTextContent("false");
  });

  it("opens the first persona with no link", () => {
    render(<PersonasPanel />);
    expect(screen.getByTestId("editing")).toHaveTextContent("analyst");
  });

  it("registers an unsaved draft with the navigation guard until it is saved (#2268)", () => {
    render(<PersonasPanel />);
    expect(hasUnsavedChanges()).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "edit" }));
    expect(hasUnsavedChanges()).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "saved" }));
    expect(hasUnsavedChanges()).toBe(false);
  });
});

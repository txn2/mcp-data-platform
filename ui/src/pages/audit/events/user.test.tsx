import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, within } from "@testing-library/react";
import type { AuditEvent, AuditFiltersResponse } from "@/api/admin/types";

import { EventsTable } from "./EventsTable";
import { EventFilters, type EventFilterState } from "./EventFilters";

const OWNER = "analyst@example.com";
const SUBJECT = "a233eaf7-fd39-4e53-8086-b264c1f82d2a";

// A person, two of the scripts they own, and a key: the shape that put the
// same address on four rows of the dropdown (#1523).
const FILTERS: AuditFiltersResponse = {
  users: [SUBJECT, "apikey:nightly-load", "script:acme-revenue-pulse", "script:acme-top-stores-drop"],
  user_labels: {
    [SUBJECT]: OWNER,
    "apikey:nightly-load": "nightly-load@apikey.local",
    "script:acme-revenue-pulse": OWNER,
    "script:acme-top-stores-drop": OWNER,
  },
  tools: [],
  toolkit_kinds: [],
  sources: [],
};

const NO_FILTERS: EventFilterState = {
  search: "",
  sessionId: "",
  userId: "",
  toolName: "",
  toolkitKind: "",
  source: "",
  success: "",
};

function event(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id: "evt-1",
    timestamp: "2026-08-16T10:30:00Z",
    duration_ms: 152,
    request_id: "req-1",
    session_id: "dps_9f2c1a4b",
    user_id: SUBJECT,
    user_email: OWNER,
    persona: "analyst",
    tool_name: "trino_query",
    toolkit_kind: "trino",
    toolkit_name: "prod",
    connection: "acme-warehouse",
    parameters: {},
    success: true,
    response_chars: 120,
    request_chars: 40,
    content_blocks: 1,
    transport: "http",
    source: "mcp",
    enrichment_applied: true,
    authorized: true,
    ...overrides,
  };
}

function table(events: AuditEvent[]) {
  return render(
    <EventsTable
      events={events}
      isLoading={false}
      sortBy="timestamp"
      sortOrder="desc"
      onSort={vi.fn()}
      onSelect={vi.fn()}
      titleMap={{}}
    />,
  );
}

function openUserFacet(onChange = vi.fn()) {
  render(
    <EventFilters
      filters={FILTERS}
      value={NO_FILTERS}
      onChange={onChange}
      onExport={vi.fn()}
      canExport={false}
    />,
  );
  // jsdom has no PointerEvent, so the trigger's pointerdown handler never runs
  // and the listbox is opened from the keyboard (see ui/README.md).
  fireEvent.keyDown(screen.getByLabelText("Filter by user"), { key: "Enter" });
  return { onChange, options: screen.getAllByRole("option") };
}

afterEach(cleanup);

describe("the user facet on the events table", () => {
  it("gives the owner and each script they own a distinguishable option", () => {
    const { options } = openUserFacet();
    const labels = options.map((o) => o.textContent);

    expect(new Set(labels).size).toBe(labels.length);
    expect(labels).toContain(OWNER);
    expect(labels).toContain(`script: acme-revenue-pulse - ${OWNER}`);
    expect(labels).toContain(`script: acme-top-stores-drop - ${OWNER}`);
    expect(labels).toContain("apikey: nightly-load");
  });

  it("filters on the principal behind the option, not on the address it shows", () => {
    const { onChange, options } = openUserFacet();

    fireEvent.click(options.find((o) => o.textContent?.includes("acme-revenue-pulse"))!);
    expect(onChange).toHaveBeenCalledWith({ userId: "script:acme-revenue-pulse" });
  });
});

describe("who a row is attributed to", () => {
  it("marks a script run as one and names the owner it acts for", () => {
    const { container } = table([
      event({ user_id: "script:acme-revenue-pulse", user_email: OWNER }),
    ]);

    expect(screen.getByText("script")).toBeTruthy();
    expect(screen.getByText(`acme-revenue-pulse - ${OWNER}`)).toBeTruthy();
    expect(
      container.querySelector(`[title="script: acme-revenue-pulse - ${OWNER}"]`),
    ).toBeTruthy();
  });

  it("leaves a person as their address, with no principal marking and no subject", () => {
    const { container } = table([event()]);
    const row = within(container.querySelector("tbody tr")!);

    expect(row.getByText(OWNER)).toBeTruthy();
    expect(row.queryByText("script")).toBeNull();
    expect(container.innerHTML).not.toContain(SUBJECT);
  });
});

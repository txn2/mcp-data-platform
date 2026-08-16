import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import type { AuditEvent } from "@/api/admin/types";

// The drawer resolves tool schemas to decide whether "Replay in Inspector" is
// enabled; that lookup is irrelevant here, so it is stubbed to an empty catalog.
vi.mock("@/api/admin/hooks", () => ({
  useToolSchemas: () => ({ data: { schemas: {} } }),
}));

import { EventsTable } from "./EventsTable";
import { EventDrawer } from "@/components/EventDrawer";
import { toCSV } from "./exportEvents";

const PURPOSE =
  "Checking whether order volume fell in the western region for the board deck.";

function event(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id: "evt-1",
    timestamp: "2026-08-16T10:30:00Z",
    duration_ms: 152,
    request_id: "req-1",
    session_id: "dps_abc",
    user_id: "analyst@example.com",
    user_email: "analyst@example.com",
    persona: "analyst",
    tool_name: "trino_query",
    toolkit_kind: "trino",
    toolkit_name: "prod",
    connection: "acme-warehouse",
    purpose: PURPOSE,
    parameters: { sql: "SELECT 1" },
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

function renderTable(events: AuditEvent[], onSelect = vi.fn()) {
  render(
    <EventsTable
      events={events}
      isLoading={false}
      sortBy="timestamp"
      sortOrder="desc"
      onSort={vi.fn()}
      onSelect={onSelect}
      titleMap={{}}
    />,
  );
  return onSelect;
}

afterEach(cleanup);

describe("purpose in the audit events table", () => {
  it("shows the stated purpose in its own column", () => {
    renderTable([event()]);
    expect(screen.getByRole("columnheader", { name: "Purpose" })).toBeTruthy();
    expect(screen.getByText(PURPOSE)).toBeTruthy();
  });

  it("renders a dash for a call that could not state one", () => {
    // A portal run, a script run, and the REST shim all record no purpose; the
    // column must read as "none stated", not as a blank cell.
    renderTable([event({ purpose: undefined, source: "admin" })]);
    expect(screen.queryByText(PURPOSE)).toBeNull();
    expect(screen.getByText("-")).toBeTruthy();
  });

  it("does not offer to sort on it", () => {
    // Alphabetical order over free prose means nothing; search covers purpose
    // instead, so the header must not be a sort control.
    renderTable([event()]);
    const header = screen.getByRole("columnheader", { name: "Purpose" });
    expect(header.querySelector("button")).toBeNull();
  });

  it("opens the drawer on row click, as every other column does", () => {
    const onSelect = renderTable([event()]);
    fireEvent.click(screen.getByText(PURPOSE));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });
});

describe("purpose in the event drawer", () => {
  it("shows the full purpose above the parameters", () => {
    const { container } = render(
      <EventDrawer event={event()} onClose={vi.fn()} />,
    );
    expect(screen.getByText("Purpose")).toBeTruthy();
    expect(screen.getByText(PURPOSE)).toBeTruthy();

    const text = container.textContent ?? "";
    expect(text.indexOf("Purpose")).toBeLessThan(text.indexOf("Parameters"));
  });

  it("omits the section entirely when no purpose was stated", () => {
    render(<EventDrawer event={event({ purpose: "" })} onClose={vi.fn()} />);
    expect(screen.queryByText("Purpose")).toBeNull();
  });
});

describe("purpose in the CSV export", () => {
  it("is a quoted column, since it carries commas", () => {
    const lines = toCSV([event()]).split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[0]!.split(",")).toContain("purpose");
    expect(lines[1]!).toContain(`"${PURPOSE}"`);
  });

  it("exports an empty quoted field when none was stated", () => {
    expect(toCSV([event({ purpose: undefined })])).toContain(',"",');
  });
});

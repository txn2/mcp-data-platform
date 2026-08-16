import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import type { AuditEvent } from "@/api/admin/types";

// The drawer resolves tool schemas to decide whether "Replay in Inspector" is
// enabled; that lookup is irrelevant here, so it is stubbed to an empty catalog.
vi.mock("@/api/admin/hooks", () => ({
  useToolSchemas: () => ({ data: { schemas: {} } }),
}));

import { EventDrawer } from "@/components/EventDrawer";
import { EventFilters, type EventFilterState } from "./EventFilters";

const SESSION_ID = "dps_9f2c1a4b8e7d6c5a4b3e2d1c0f9e8a7b";

function event(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id: "evt-1",
    timestamp: "2026-08-16T10:30:00Z",
    duration_ms: 152,
    request_id: "req-1",
    session_id: SESSION_ID,
    user_id: "analyst@example.com",
    user_email: "analyst@example.com",
    persona: "analyst",
    tool_name: "trino_query",
    toolkit_kind: "trino",
    toolkit_name: "prod",
    connection: "acme-warehouse",
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

const NO_FILTERS: EventFilterState = {
  search: "",
  sessionId: "",
  userId: "",
  toolName: "",
  toolkitKind: "",
  source: "",
  success: "",
};

afterEach(cleanup);

describe("the session in the event drawer", () => {
  it("is a way to the session, not just an id to copy", () => {
    const onNavigate = vi.fn();
    render(
      <EventDrawer event={event()} onClose={vi.fn()} onNavigate={onNavigate} />,
    );

    fireEvent.click(screen.getByText(SESSION_ID));
    expect(onNavigate).toHaveBeenCalledWith(`/admin/sessions/${SESSION_ID}`);
  });

  it("stays plain text where there is nowhere to navigate", () => {
    const { container } = render(
      <EventDrawer event={event()} onClose={vi.fn()} />,
    );
    const buttons = [...container.querySelectorAll("button")].map(
      (b) => b.textContent,
    );
    expect(buttons).not.toContain(SESSION_ID);
    expect(screen.getByText(SESSION_ID)).toBeTruthy();
  });
});

describe("the session filter on the events table", () => {
  it("narrows the table to one session's calls", () => {
    const onChange = vi.fn();
    render(
      <EventFilters
        value={NO_FILTERS}
        onChange={onChange}
        onExport={vi.fn()}
        canExport={false}
      />,
    );

    fireEvent.change(screen.getByLabelText("Filter by session ID"), {
      target: { value: SESSION_ID },
    });
    expect(onChange).toHaveBeenCalledWith({ sessionId: SESSION_ID });
  });
});

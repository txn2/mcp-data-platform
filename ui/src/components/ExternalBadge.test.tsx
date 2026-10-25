import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, within } from "@testing-library/react";
import type { AuditEvent, DirectoryUser } from "@/api/admin/types";

// The external mark (#2290) wherever people are chosen or listed: the share
// picker, Admin > Users and the audit drawer.

const { directory, personas } = vi.hoisted(() => ({
  directory: { users: [] as { email: string; first_name?: string; confirmed: boolean; external?: boolean }[] },
  personas: { list: [] as { name: string; external?: boolean }[] },
}));

vi.mock("@/api/portal/hooks", () => ({
  useDirectoryUsers: () => ({ data: { users: directory.users, total: directory.users.length } }),
}));
vi.mock("@/api/admin/hooks", () => ({
  useToolSchemas: () => ({ data: { schemas: {} } }),
  usePersonas: (enabled: boolean) => ({ data: enabled ? { personas: personas.list } : undefined }),
  useUpdateUser: () => ({ mutate: vi.fn(), isPending: false }),
}));

import { UserPicker } from "./UserPicker";
import { UserRow } from "@/pages/settings/users/UserRow";
import { EventDrawer } from "./EventDrawer";

afterEach(cleanup);

describe("the share picker", () => {
  it("marks each person outside the organization", () => {
    directory.users = [
      { email: "dana@client-a.example.com", first_name: "Dana", confirmed: true, external: true },
      { email: "ann@example.com", first_name: "Ann", confirmed: true },
    ];
    render(<UserPicker value="" onChange={vi.fn()} />);
    fireEvent.focus(screen.getByRole("combobox"));
    const options = screen.getAllByRole("option");
    expect(within(options[0]!).getByText("External")).toBeTruthy();
    expect(within(options[1]!).queryByText("External")).toBeNull();
  });
});

function user(overrides: Partial<DirectoryUser> = {}): DirectoryUser {
  return { email: "dana@client-a.example.com", first_name: "Dana", last_name: "Reyes", source: "auth", confirmed: true, created_at: "", updated_at: "", ...overrides } as DirectoryUser;
}

function renderRow(u: DirectoryUser) {
  render(
    <table>
      <tbody>
        <UserRow user={u} isReadOnly deleteConfirm={false} deleting={false} onRequestDelete={vi.fn()} onCancelDelete={vi.fn()} onConfirmDelete={vi.fn()} />
      </tbody>
    </table>,
  );
}

describe("Admin > Users", () => {
  it("shows the persona and marks an external person", () => {
    renderRow(user({ persona: "client-a", external: true }));
    expect(screen.getByText("client-a")).toBeTruthy();
    expect(screen.getByText("External")).toBeTruthy();
  });

  it("marks nobody else", () => {
    renderRow(user({ email: "ann@example.com", persona: "staff" }));
    expect(screen.getByText("staff")).toBeTruthy();
    expect(screen.queryByText("External")).toBeNull();
  });

  it("shows no persona for someone whose roles resolve to none", () => {
    renderRow(user({ email: "pre@example.com", confirmed: false }));
    expect(screen.getAllByText("--").length).toBeGreaterThan(0);
  });
});

function event(persona: string): AuditEvent {
  return { id: "e", timestamp: "2026-10-24T10:00:00Z", user_id: "u", user_email: "u@example.com", persona, tool_name: "fetch", toolkit_kind: "search", toolkit_name: "search", success: true, duration_ms: 1, response_chars: 1, request_chars: 1, content_blocks: 1, parameters: {} } as AuditEvent;
}

describe("the audit drawer", () => {
  it("marks an external persona", () => {
    personas.list = [{ name: "client-a", external: true }, { name: "staff" }];
    render(<EventDrawer event={event("client-a")} onClose={vi.fn()} />);
    expect(screen.getByText("External")).toBeTruthy();
  });

  it("marks no internal persona, and reads no persona list on a user's surface", () => {
    personas.list = [{ name: "client-a", external: true }];
    render(<EventDrawer event={event("staff")} onClose={vi.fn()} />);
    expect(screen.queryByText("External")).toBeNull();
    cleanup();
    render(<EventDrawer event={event("client-a")} onClose={vi.fn()} replayable={false} />);
    expect(screen.queryByText("External")).toBeNull();
  });
});

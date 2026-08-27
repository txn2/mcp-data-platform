import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore, type UserProfile } from "@/stores/auth";
import type { Resource } from "@/api/resources/types";
import { EditModal } from "./EditModal";

// A resource's library used to be chosen once, on the upload form, and never
// again (#1502). It is now a field on the edit dialog, offering the libraries
// this caller may move to and nothing else -- so the form never asks for a move
// the server will refuse.

const RESOURCE: Resource = {
  id: "res-1",
  scope: "user",
  scope_id: "analyst@example.com",
  category: "templates",
  filename: "report.docx",
  display_name: "Report",
  description: "the template",
  mime_type: "text/plain",
  size_bytes: 12,
  s3_key: "resources/user/analyst@example.com/res-1/report.docx",
  uri: "mcp://user/analyst@example.com/templates/report.docx",
  tags: [],
  uploader_sub: "analyst@example.com",
  uploader_email: "analyst@example.com",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

function signIn(overrides: Partial<UserProfile> = {}) {
  useAuthStore.setState({
    user: {
      user_id: "analyst@example.com",
      email: "analyst@example.com",
      roles: ["dp_analyst"],
      is_admin: false,
      persona: "ops",
      ...overrides,
    },
  });
}

let patched: Record<string, unknown> | null = null;

function stubApi() {
  patched = null;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PATCH") {
        patched = JSON.parse(String(init.body)) as Record<string, unknown>;
        return new Response(JSON.stringify(RESOURCE), { status: 200 });
      }
      if (url.includes("/personas")) {
        return new Response(JSON.stringify({ personas: [{ name: "finance" }] }), { status: 200 });
      }
      return new Response("{}", { status: 200 });
    }),
  );
}

function renderModal(admin = false) {
  stubApi();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <EditModal resource={RESOURCE} admin={admin} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  useAuthStore.setState({ user: null });
});

async function openLibraryPicker() {
  fireEvent.click(screen.getByRole("combobox", { name: "Library" }));
  await waitFor(() => expect(screen.getByRole("listbox")).toBeTruthy());
}

async function pickLibrary(name: string) {
  await openLibraryPicker();
  fireEvent.click(await screen.findByRole("option", { name }));
}

describe("the library field on the edit dialog", () => {
  it("moves the file into a persona the owner belongs to", async () => {
    signIn();
    renderModal();

    await pickLibrary("ops persona");
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => expect(patched).not.toBeNull());
    expect(patched).toEqual({ scope: "persona", scope_id: "ops" });
  });

  it("says who will see the file and that its address changes", async () => {
    signIn();
    renderModal();

    await pickLibrary("ops persona");
    const note = screen.getByTestId("library-move-note").textContent ?? "";
    expect(note).toContain("ops persona can see it");
    // The URI change is the part nobody expects, so the note says outright that
    // the address already written down keeps working.
    expect(note).toContain("URI changes");
    expect(note).toContain("the URI it is leaving keeps resolving");
  });

  it("says nothing about a move while the file stays where it is", () => {
    signIn();
    renderModal();
    expect(screen.queryByTestId("library-move-note")).toBeNull();
  });

  it("sends no scope when only the metadata changed", async () => {
    signIn();
    renderModal();

    fireEvent.change(screen.getByLabelText("Display Name"), { target: { value: "Renamed" } });
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => expect(patched).not.toBeNull());
    // Echoing the current library back would read, in the audit trail, as
    // somebody having refiled the file.
    expect(patched).toEqual({ display_name: "Renamed" });
  });

  it("is absent when there is nowhere to move the file", () => {
    signIn({ persona: undefined, roles: [] });
    renderModal();
    expect(screen.queryByRole("combobox", { name: "Library" })).toBeNull();
  });

  it("asks an administrator for the address when they pick a person's library", async () => {
    signIn({ is_admin: true });
    renderModal(true);

    await pickLibrary("A person's library...");
    fireEvent.click(screen.getByText("Save"));
    // Nothing is sent until the person is named.
    expect(patched).toBeNull();
    expect(screen.getByText(/Name the person/)).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Person's email"), {
      target: { value: " her@example.com " },
    });
    fireEvent.click(screen.getByText("Save"));

    await waitFor(() => expect(patched).not.toBeNull());
    expect(patched).toEqual({ scope: "user", scope_id: "her@example.com" });
  });

  it("offers an administrator the global library and every persona", async () => {
    signIn({ is_admin: true });
    renderModal(true);

    await openLibraryPicker();
    expect(screen.getByRole("option", { name: "Global" })).toBeTruthy();
    // The deployment's persona list is fetched, so the option arrives with it.
    expect(await screen.findByRole("option", { name: "finance persona" })).toBeTruthy();
  });

  it("withholds the global library on the reader's own page", async () => {
    // Same rule the Upload control applies: publishing to everyone signed in is
    // not offered inside a page reached by browsing.
    signIn({ is_admin: true });
    renderModal(false);

    await openLibraryPicker();
    expect(screen.getByRole("option", { name: "ops persona" })).toBeTruthy();
    expect(screen.queryByRole("option", { name: "Global" })).toBeNull();
  });
});

import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore, type UserProfile } from "@/stores/auth";
import { UploadModal } from "./UploadModal";
import type { ScopeTarget } from "../scopes";

// The dialog reports each fan-out target by name. It used to report it by
// scope id, so a failed upload to the caller's own library was announced by
// their subject identifier, which names nobody to the person reading it
// (#1525).

const SUBJECT = "eb4af2b1-1a6c-4e25-b692-da2e9b174ef7";
const REFUSED = "The storage backend did not accept the file. Nothing was saved.";

function signIn(overrides: Partial<UserProfile> = {}) {
  useAuthStore.setState({
    user: {
      user_id: SUBJECT,
      email: "analyst@example.com",
      roles: ["dp_analyst"],
      is_admin: false,
      persona: "ops",
      ...overrides,
    },
  });
}

// stubApi refuses every upload the way the resource handler refuses one blob
// storage would not take: a 5xx whose body carries the message the reader sees.
function stubApi() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST") {
        return new Response(JSON.stringify({ error: REFUSED }), { status: 503 });
      }
      if (url.includes("/personas")) {
        return new Response(JSON.stringify({ personas: [{ name: "finance" }] }), { status: 200 });
      }
      return new Response("{}", { status: 200 });
    }),
  );
}

function renderModal(admin: boolean, destination: ScopeTarget | null) {
  stubApi();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <UploadModal
        admin={admin}
        personaNames={["finance", "ops"]}
        destination={destination}
        folder="samples"
        folders={["samples", "data/media-manager"]}
        onClose={() => {}}
      />
    </QueryClientProvider>,
  );
}

// fillDraft supplies the three fields rejectDraft requires, so the submit
// reaches the upload rather than stopping at local validation.
function fillDraft(container: HTMLElement) {
  fireEvent.change(screen.getByLabelText("Display Name"), { target: { value: "Q4 export" } });
  fireEvent.change(screen.getByLabelText("Description"), { target: { value: "the export" } });
  const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(fileInput, {
    target: { files: [new File(["a,b\n1,2\n"], "export.csv", { type: "text/csv" })] },
  });
}

async function submitAndReadAlert(container: HTMLElement): Promise<string> {
  fillDraft(container);
  fireEvent.click(screen.getByRole("button", { name: "Upload" }));
  const alert = await screen.findByRole("alert");
  await waitFor(() => expect(alert.textContent).toContain(REFUSED));
  return alert.textContent ?? "";
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  useAuthStore.setState({ user: null });
});

describe("how the upload dialog names the library it failed to write to", () => {
  it("names the caller's own library, not their subject identifier", async () => {
    signIn();
    const { container } = renderModal(false, { scope: "user", scope_id: SUBJECT });

    const text = await submitAndReadAlert(container);

    expect(text).toContain("My Resources");
    // The defect: the identifier reached the dialog and stood in for the
    // library's name.
    expect(text).not.toContain(SUBJECT);
  });

  it("names the global library by its name", async () => {
    signIn({ is_admin: true, roles: ["admin"] });
    const { container } = renderModal(true, null);

    const text = await submitAndReadAlert(container);

    expect(text).toContain("Global");
  });

  it("names each addressed library on the administrator's fan-out", async () => {
    signIn({ is_admin: true, roles: ["admin"] });
    const { container } = renderModal(true, null);

    fireEvent.click(screen.getByRole("combobox", { name: "Scope" }));
    fireEvent.click(await screen.findByRole("option", { name: "User" }));
    fireEvent.change(screen.getByLabelText("User emails (comma-separated)"), {
      target: { value: "one@example.com, two@example.com" },
    });

    const text = await submitAndReadAlert(container);

    // Both halves of a fan-out are reported, so both have to be identifiable.
    expect(text).toContain("one@example.com's library");
    expect(text).toContain("two@example.com's library");
  });

  it("names a persona library the caller was sent to", async () => {
    signIn({ roles: ["dp_persona-admin:finance"] });
    const { container } = renderModal(false, { scope: "persona", scope_id: "finance" });

    const text = await submitAndReadAlert(container);

    expect(text).toContain("finance");
    expect(text).not.toContain(SUBJECT);
  });
});

// Issue #1628: the dialog held its own copy of the 100 MB constant, in the
// label, in the refusal, and in the check behind it. A deployment that raised
// resources.managed.max_upload_bytes had a file chooser still announcing the
// old number and refusing files the server would have taken.
describe("the size ceiling the upload dialog states and enforces", () => {
  // pickFile puts a file of the given size in the chooser without allocating
  // it: only the reported size is read.
  function pickFile(container: HTMLElement, size: number) {
    const file = new File(["x"], "big.csv", { type: "text/csv" });
    Object.defineProperty(file, "size", { value: size });
    const input = container.querySelector('input[type="file"]') as HTMLInputElement;
    fireEvent.change(input, { target: { files: [file] } });
  }

  it("states the deployment's own ceiling in the file chooser", () => {
    signIn({ max_upload_bytes: 262144000 });
    renderModal(false, { scope: "user", scope_id: SUBJECT });

    expect(screen.getByText("Choose file (max 250 MB)")).toBeTruthy();
  });

  it("falls back to the shipped default when the server reports no ceiling", () => {
    signIn();
    renderModal(false, { scope: "user", scope_id: SUBJECT });

    expect(screen.getByText("Choose file (max 100 MB)")).toBeTruthy();
  });

  it("accepts a file the raised ceiling admits, which the old constant refused", async () => {
    signIn({ max_upload_bytes: 262144000 });
    const { container } = renderModal(false, { scope: "user", scope_id: SUBJECT });

    fireEvent.change(screen.getByLabelText("Display Name"), { target: { value: "Q4 export" } });
    fireEvent.change(screen.getByLabelText("Description"), { target: { value: "the export" } });
    pickFile(container, 200 * 1024 * 1024);
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    // It reaches the server, which is what the stubbed API refuses. A local
    // size rejection would never get that far.
    const alert = await screen.findByRole("alert");
    await waitFor(() => expect(alert.textContent).toContain(REFUSED));
  });

  it("refuses past the raised ceiling by naming that deployment's number", async () => {
    signIn({ max_upload_bytes: 262144000 });
    const { container } = renderModal(false, { scope: "user", scope_id: SUBJECT });

    fireEvent.change(screen.getByLabelText("Display Name"), { target: { value: "Q4 export" } });
    fireEvent.change(screen.getByLabelText("Description"), { target: { value: "the export" } });
    pickFile(container, 262144000 + 1);
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("File exceeds 250 MB limit");
    expect(alert.textContent).not.toContain("100 MB");
  });
});

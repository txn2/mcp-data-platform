import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore } from "@/stores/auth";
import { BulkUploadModal } from "./BulkUploadModal";
import type { ScopeTarget } from "../scopes";
import type { Sender } from "../bulk/useBulkUpload";
import type { SendResult } from "../bulk/send";

// The many-files upload (#1862): each file is its own request, and what the
// server did with each is reported against that file. The sender is injected,
// so these tests read what the dialog sent and decide what the server answered.

const SUBJECT = "eb4af2b1-1a6c-4e25-b692-da2e9b174ef7";

function signIn() {
  useAuthStore.setState({
    user: { user_id: SUBJECT, email: "analyst@example.com", roles: ["dp_analyst"], is_admin: false, persona: "ops" },
  });
}

/** answerBy answers each file by its name; the forms it was sent are kept. */
function answerBy(answers: Record<string, SendResult>) {
  const sent: FormData[] = [];
  const created: SendResult = { ok: true, status: 201, outcome: "created" };
  const send: Sender = vi.fn(async (form: FormData, onProgress: (fraction: number) => void) => {
    sent.push(form);
    onProgress(0.5);
    const name = (form.get("file") as File).name;
    return answers[name] ?? created;
  });
  return { send, sent };
}

function renderModal(
  send: Sender,
  destination: ScopeTarget | null = { scope: "user", scope_id: SUBJECT },
  folder = "brand",
) {
  signIn();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onClose = vi.fn();
  render(
    <QueryClientProvider client={qc}>
      <BulkUploadModal
        onClose={onClose}
        personaNames={["ops"]}
        destination={destination}
        folder={folder}
        folders={["brand"]}
        send={send}
      />
    </QueryClientProvider>,
  );
  return { onClose };
}

async function pick(names: string[]) {
  const files = names.map((n) => new File(["x"], n));
  await act(async () => {
    fireEvent.change(screen.getByTestId("bulk-files-input"), { target: { files } });
  });
}

async function upload(label: RegExp | string) {
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: label }));
  });
}

// jsdom lays nothing out, so the file list's viewport measures zero and the
// virtualizer draws no rows. Give every element the list's own size.
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(320);
  vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockReturnValue(640);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("BulkUploadModal", () => {
  it("offers nothing to send until files are chosen", () => {
    const { send } = answerBy({});
    renderModal(send);
    expect(screen.getByTestId("bulk-empty")).toHaveTextContent("No files chosen yet.");
    expect(screen.getByRole("button", { name: "Upload 0 files" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Clear list" })).toBeDisabled();
  });

  it("reports each file's outcome and the batch, and never sends a refused file", async () => {
    const { send, sent } = answerBy({
      "b.png": { ok: true, status: 200, outcome: "revised" },
      "c.png": { ok: true, status: 200, outcome: "unchanged" },
      "d.png": { ok: false, status: 503, error: "The storage backend did not accept the file." },
    });
    renderModal(send);
    await pick(["a.png", "b.png", "c.png", "d.png", "setup.exe"]);
    expect(screen.getAllByTestId("bulk-row")).toHaveLength(5);
    expect(screen.getAllByTestId("web-image-badge")).toHaveLength(4);

    await upload("Upload 4 files");

    await waitFor(() => expect(screen.getByTestId("bulk-summary")).toBeInTheDocument());
    expect(screen.getByTestId("bulk-summary")).toHaveTextContent(
      "1 created, 1 new version, 1 unchanged, 1 failed, 1 not sent",
    );
    expect(sent).toHaveLength(4);
    expect(sent.map((f) => f.get("path"))).toEqual(["brand", "brand", "brand", "brand"]);
    expect(sent[0]?.get("description")).toBe("a");
    expect(sent[0]?.get("if_exists")).toBe("skip_unchanged");
    const outcomes = screen.getAllByTestId("bulk-outcome").map((b) => b.textContent);
    expect(outcomes).toEqual(["Created", "New version", "Unchanged"]);
    const failures = screen.getAllByTestId("bulk-failure").map((f) => f.textContent);
    expect(failures).toEqual([
      "FailedThe storage backend did not accept the file.",
      "Not sentFiles ending in .exe are not accepted.",
    ]);
    expect(screen.getByText("Close", { selector: "button" })).toBeInTheDocument();
  });

  it("retries only the failed files when every file failed", async () => {
    let fail = true;
    const sent: string[] = [];
    const send: Sender = async (form) => {
      sent.push((form.get("file") as File).name);
      return fail ? { ok: false, status: 0, error: "The connection closed before the server answered." } : { ok: true, status: 201, outcome: "created" };
    };
    renderModal(send);
    await pick(["a.png", "b.png"]);
    await upload("Upload 2 files");
    await waitFor(() => expect(screen.getByTestId("bulk-summary")).toHaveTextContent("0 created, 0 new versions, 0 unchanged, 2 failed"));

    fail = false;
    await upload("Retry failed");
    await waitFor(() => expect(screen.getByTestId("bulk-summary")).toHaveTextContent("2 created, 0 new versions, 0 unchanged, 0 failed"));
    expect(sent).toEqual(["a.png", "b.png", "a.png", "b.png"]);
    expect(screen.queryByRole("button", { name: "Retry failed" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Upload 0 files" })).toBeDisabled();
  });

  it("sends the edited display name, the template and the shared tags", async () => {
    const { send, sent } = answerBy({});
    renderModal(send);
    await pick(["Acme Logo.png"]);
    fireEvent.change(screen.getByLabelText("Display name for Acme Logo.png"), { target: { value: "Acme mark" } });
    fireEvent.change(screen.getByLabelText(/Description/), { target: { value: "{name}, colour" } });
    fireEvent.change(screen.getByLabelText(/Tags for every file/), { target: { value: "Brand, logo" } });
    await upload("Upload 1 file");
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.get("display_name")).toBe("Acme mark");
    expect(sent[0]?.get("description")).toBe("Acme mark, colour");
    expect(sent[0]?.getAll("tags")).toEqual(["brand", "logo"]);
    expect((sent[0]?.get("file") as File).name).toBe("acme-logo.png");
  });

  it("refuses a base folder the server would refuse, before sending", async () => {
    const { send, sent } = answerBy({});
    renderModal(send, { scope: "user", scope_id: SUBJECT }, "Bad Folder");
    await pick(["a.png"]);
    await upload("Upload 1 file");
    expect(screen.getByRole("alert")).toHaveTextContent("must be lowercase letters, digits and hyphens");
    expect(sent).toHaveLength(0);
  });

  it("asks for a library on the All view and clears the list", async () => {
    const { send, sent } = answerBy({});
    renderModal(send, null);
    expect(screen.getByTestId("upload-destination-picker")).toBeInTheDocument();
    await pick(["a.png"]);
    await upload("Upload 1 file");
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]?.get("scope")).toBe("user");
    fireEvent.click(screen.getByRole("button", { name: "Clear list" }));
    expect(screen.getByTestId("bulk-empty")).toBeInTheDocument();
    expect(screen.queryByTestId("bulk-summary")).not.toBeInTheDocument();
  });

  it("takes a drop and a folder pick, and unpacks an archive only when asked", async () => {
    const { send } = answerBy({});
    const unzip = vi.fn(async () => ({ "logos/a.png": new Uint8Array([1]), "logos/": new Uint8Array() }));
    signIn();
    const qc = new QueryClient();
    render(
      <QueryClientProvider client={qc}>
        <BulkUploadModal onClose={() => {}} personaNames={[]} destination={null} folder="" folders={[]} send={send} unzip={unzip} />
      </QueryClientProvider>,
    );
    const zone = screen.getByTestId("bulk-drop-zone");
    fireEvent.dragOver(zone);
    fireEvent.dragLeave(zone);
    await act(async () => {
      fireEvent.drop(zone, { dataTransfer: { items: [], files: [new File(["d"], "dropped.txt")] } });
    });
    expect(screen.getByLabelText("Display name for dropped.txt")).toBeInTheDocument();

    const kit = new File([new Uint8Array([80, 75])], "kit.zip");
    Object.defineProperty(kit, "arrayBuffer", { value: async () => new ArrayBuffer(2) });
    await act(async () => {
      fireEvent.change(screen.getByTestId("bulk-folder-input"), { target: { files: [kit] } });
    });
    expect(unzip).toHaveBeenCalledTimes(1);
    expect(screen.getByText("samples/logos/a.png · 1 B", { exact: false })).toBeInTheDocument();

    fireEvent.click(screen.getByTestId("bulk-unpack"));
    await act(async () => {
      fireEvent.change(screen.getByTestId("bulk-files-input"), { target: { files: [kit] } });
    });
    expect(unzip).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("Display name for kit.zip")).toBeInTheDocument();
  });

  it("stops starting files when asked, and lets the ones in flight finish", async () => {
    const release: (() => void)[] = [];
    const sent: string[] = [];
    const send: Sender = (form) =>
      new Promise((resolve) => {
        sent.push((form.get("file") as File).name);
        release.push(() => resolve({ ok: true, status: 201, outcome: "created" }));
      });
    renderModal(send);
    await pick(["a.png", "b.png", "c.png", "d.png", "e.png", "f.png"]);
    await upload("Upload 6 files");
    expect(sent).toHaveLength(4);
    expect(screen.getAllByTestId("bulk-progress")).toHaveLength(4);
    fireEvent.click(screen.getByRole("button", { name: "Stop after current files" }));
    await act(async () => {
      for (const r of release.splice(0)) r();
    });
    await waitFor(() => expect(screen.getByTestId("bulk-summary")).toHaveTextContent("4 created"));
    expect(sent).toHaveLength(4);
    expect(screen.getByRole("button", { name: "Upload 2 files" })).toBeEnabled();
  });
});

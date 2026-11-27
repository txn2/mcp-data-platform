import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ResourceRoot } from "../scopes";

// The link hooks are this module's seams: what each dialog sends, and what it
// shows for each answer (#2844).
const api = vi.hoisted(() => ({
  create: { mutate: vi.fn(), isPending: false, data: undefined as unknown, error: null as unknown },
  revoke: { mutate: vi.fn(), isPending: false },
  links: { data: [] as unknown[], isLoading: false, error: null as unknown },
}));
vi.mock("@/api/resources/links", () => ({
  useCreateUploadLink: () => api.create,
  useRevokeUploadLink: () => api.revoke,
  useUploadLinks: () => api.links,
}));

import { linkFolder, NewUploadLinkDialog, untilText, UploadLinksDialog } from "./LinkDialogs";

const root: ResourceRoot = {
  key: "user",
  label: "My Resources",
  target: { scope: "user", scope_id: "u-1" },
  params: { scope: "user" },
};

const inHours = (h: number) => new Date(Date.now() + h * 3600_000).toISOString();

beforeEach(() => {
  api.create = { mutate: vi.fn(), isPending: false, data: undefined, error: null };
  api.revoke = { mutate: vi.fn(), isPending: false };
  api.links = { data: [], isLoading: false, error: null };
});
afterEach(cleanup);

describe("the helpers", () => {
  it("says how long until an expiry, and where a link files", () => {
    const now = Date.parse("2026-11-27T12:00:00Z");
    expect(untilText("2026-11-27T12:12:00Z", now)).toBe("in 12 minutes");
    expect(untilText("2026-11-27T12:01:00Z", now)).toBe("in 1 minute");
    expect(untilText("2026-11-27T17:00:00Z", now)).toBe("in 5 hours");
    expect(untilText("2026-11-27T13:00:00Z", now)).toBe("in 1 hour");
    expect(untilText("2026-11-27T11:00:00Z", now)).toBe("in 0 minutes");
    expect(linkFolder({ scope: "user", path: "gpulab/renders" })).toBe("/My Resources/gpulab/renders");
    expect(linkFolder({ scope: "global", path: "", filename: "a.csv" })).toBe("/Global/a.csv");
    expect(linkFolder({ scope: "persona", scope_id: "analyst", path: "x" })).toBe("/analyst/x");
    expect(linkFolder({ scope: "persona", path: "x" })).toBe("/Persona/x");
  });
});

describe("New upload link", () => {
  it("sends the folder, type, size, file count, lifetime and naming the form holds", () => {
    render(<NewUploadLinkDialog root={root} folder="gpulab/renders" onClose={vi.fn()} />);
    expect((screen.getByLabelText("Folder in /My Resources") as HTMLInputElement).value).toBe("gpulab/renders");
    const make = screen.getByRole("button", { name: "Make link" }) as HTMLButtonElement;
    expect(make.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("File type"), { target: { value: "image/png" } });
    fireEvent.change(screen.getByLabelText("Files"), { target: { value: "4" } });
    fireEvent.change(screen.getByLabelText("Expires after"), { target: { value: "86400" } });
    fireEvent.change(screen.getByLabelText("If a file of that name is already here"), { target: { value: "replace" } });
    expect((screen.getByRole("radio", { name: /One file, named/ }) as HTMLInputElement).disabled).toBe(true);
    fireEvent.click(make);
    expect(api.create.mutate).toHaveBeenCalledWith({
      scope: "user",
      scope_id: "u-1",
      path: "gpulab/renders",
      filename: undefined,
      content_type: "image/png",
      size_bytes: 20 * 1024 * 1024,
      max_files: 4,
      ttl_seconds: 86400,
      if_exists: "replace",
    });
  });

  it("names a single file when asked, and holds the form until it is complete", () => {
    render(<NewUploadLinkDialog root={{ ...root, target: { scope: "global", scope_id: "" } }} folder="reports" onClose={vi.fn()} />);
    fireEvent.change(screen.getByLabelText("File type"), { target: { value: "text/csv" } });
    fireEvent.click(screen.getByRole("radio", { name: /One file, named/ }));
    expect(screen.getByText("Name the file, or let the service name it.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("File name"), { target: { value: "weekly.csv" } });
    fireEvent.change(screen.getByLabelText("Largest file (MB)"), { target: { value: "x" } });
    expect(screen.getByText("Largest file is a size in MB.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Largest file (MB)"), { target: { value: "5" } });
    fireEvent.change(screen.getByLabelText("Files"), { target: { value: "500" } });
    expect(screen.getByText("Files is 1 to 100.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Files"), { target: { value: "1" } });
    fireEvent.click(screen.getByRole("button", { name: "Make link" }));
    expect(api.create.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "global", scope_id: undefined, filename: "weekly.csv", size_bytes: 5 * 1024 * 1024 }),
    );
  });

  it("asks for a folder at the library's root, where no file is filed (#2844)", () => {
    render(<NewUploadLinkDialog root={root} folder="" onClose={vi.fn()} />);
    expect(screen.getByText("Name a folder: a file is filed in a folder, for example renders.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Make link" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Folder in /My Resources"), { target: { value: "/inbox/" } });
    fireEvent.change(screen.getByLabelText("File type"), { target: { value: "text/csv" } });
    fireEvent.click(screen.getByRole("button", { name: "Make link" }));
    expect(api.create.mutate).toHaveBeenCalledWith(expect.objectContaining({ path: "inbox" }));
  });

  it("shows a refusal, and Making while it waits", () => {
    api.create = { ...api.create, error: new Error("size_bytes 524288000 is over this deployment's upload limit"), isPending: false };
    render(<NewUploadLinkDialog root={root} folder="x" onClose={vi.fn()} />);
    expect(screen.getByText(/over this deployment's upload limit/)).toBeTruthy();
    cleanup();
    api.create = { ...api.create, error: null, isPending: true };
    render(<NewUploadLinkDialog root={root} folder="x" onClose={vi.fn()} />);
    expect(screen.getByRole("button", { name: /Making/ })).toBeTruthy();
  });

  it("shows the link once, with Copy, what it takes and how a service sends a file", async () => {
    api.create = {
      ...api.create,
      data: {
        id: "ul-1",
        upload_url: "https://data.example.com/api/v1/resources/uploads/abcdefghijklmnopqrstuvwxyz",
        method: "PUT",
        expires_at: inHours(6),
        max_bytes: 20 * 1024 * 1024,
        max_files: 4,
        name_in_url: true,
      },
    };
    const writeText = vi.fn(() => Promise.resolve());
    Object.assign(navigator, { clipboard: { writeText } });
    const onClose = vi.fn();
    render(<NewUploadLinkDialog root={root} folder="gpulab/renders" onClose={onClose} />);
    expect(screen.getByRole("heading", { name: "Upload link ready" })).toBeTruthy();
    expect(screen.getByTestId("upload-link-url").textContent).toBe(
      "https://data.example.com/api/v1/resources/uploads/abcdefghijklmnopqrstuvwxyz",
    );
    expect(screen.getByText(/only time the link is shown/)).toBeTruthy();
    expect(screen.getByText(/4 files, .*up to 20 MB each/)).toBeTruthy();
    expect(screen.getByText(/in 6 hours/)).toBeTruthy();
    expect(screen.getByText(/curl --fail -T file.ext/).textContent).toContain("/abcd…uvwxyz/file.ext");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("https://data.example.com/api/v1/resources/uploads/abcdefghijklmnopqrstuvwxyz");
    expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("shows a link for one named file", () => {
    api.create = {
      ...api.create,
      data: { id: "ul-2", upload_url: "https://p/api/v1/resources/uploads/abcdefghijkl", method: "PUT", expires_at: inHours(1), max_bytes: 1024 * 1024, max_files: 1 },
    };
    render(<NewUploadLinkDialog root={root} folder="reports" onClose={vi.fn()} />);
    expect(screen.getByText("Fixed by the link")).toBeTruthy();
    expect(screen.getByText(/1 file, .*up to 1 MB$/)).toBeTruthy();
  });
});

describe("Upload links", () => {
  const rows = [
    { id: "a", scope: "user", path: "gpulab/renders", content_type: "image/png", max_bytes: 1, max_files: 4, files_used: 1, created_at: "", expires_at: inHours(5) },
    { id: "b", scope: "user", path: "reports", filename: "weekly.csv", content_type: "text/csv", max_bytes: 1, max_files: 1, files_used: 0, created_at: "", expires_at: inHours(0.2) },
  ];

  it("lists the open links and revokes one after asking", () => {
    api.links = { data: rows, isLoading: false, error: null };
    render(<UploadLinksDialog onClose={vi.fn()} onNew={vi.fn()} />);
    const found = screen.getAllByTestId("upload-link-row");
    expect(found).toHaveLength(2);
    expect(within(found[0]!).getByText("/My Resources/gpulab/renders")).toBeTruthy();
    expect(within(found[0]!).getByText("1 of 4 filed")).toBeTruthy();
    expect(within(found[1]!).getByText("/My Resources/reports/weekly.csv")).toBeTruthy();
    fireEvent.click(within(found[1]!).getByRole("button", { name: "Revoke" }));
    expect(within(found[1]!).getByText("Revoke this link?")).toBeTruthy();
    fireEvent.click(within(found[1]!).getByRole("button", { name: "Keep" }));
    expect(within(found[1]!).queryByText("Revoke this link?")).toBeNull();
    fireEvent.click(within(found[1]!).getByRole("button", { name: "Revoke" }));
    fireEvent.click(within(found[1]!).getAllByRole("button", { name: "Revoke" })[0]!);
    expect(api.revoke.mutate).toHaveBeenCalledWith("b", expect.anything());
  });

  it("says a revoked link was revoked", async () => {
    api.links = { data: rows, isLoading: false, error: null };
    api.revoke.mutate = vi.fn((_id: string, opts: { onSuccess: () => void }) => opts.onSuccess());
    render(<UploadLinksDialog onClose={vi.fn()} />);
    const first = screen.getAllByTestId("upload-link-row")[0]!;
    fireEvent.click(within(first).getByRole("button", { name: "Revoke" }));
    fireEvent.click(within(first).getAllByRole("button", { name: "Revoke" })[0]!);
    expect(await screen.findByRole("status")).toHaveProperty("textContent", "Upload link revoked");
  });

  it("says when a revoke failed", async () => {
    api.links = { data: rows, isLoading: false, error: null };
    api.revoke.mutate = vi.fn((_id: string, opts: { onError: () => void }) => opts.onError());
    render(<UploadLinksDialog onClose={vi.fn()} />);
    const first = screen.getAllByTestId("upload-link-row")[0]!;
    fireEvent.click(within(first).getByRole("button", { name: "Revoke" }));
    fireEvent.click(within(first).getAllByRole("button", { name: "Revoke" })[0]!);
    await waitFor(() => expect(screen.getByText("The link couldn't be revoked. Try again.")).toBeTruthy());
    expect(screen.queryByRole("button", { name: "New upload link..." })).toBeNull();
  });

  it("shows none, loading and a failed read", () => {
    const onNew = vi.fn();
    render(<UploadLinksDialog onClose={vi.fn()} onNew={onNew} />);
    expect(screen.getByText("No open upload links")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "New upload link..." }));
    expect(onNew).toHaveBeenCalled();
    cleanup();
    api.links = { data: [], isLoading: true, error: null };
    render(<UploadLinksDialog onClose={vi.fn()} />);
    expect(screen.getByText("Loading...")).toBeTruthy();
    cleanup();
    api.links = { data: [], isLoading: false, error: new Error("boom") };
    render(<UploadLinksDialog onClose={vi.fn()} />);
    expect(screen.getByText(/could not be read/)).toBeTruthy();
  });
});

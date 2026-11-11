import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { resetSitesMock } from "@/mocks/handlers/sites";
import { bumpSiteFile } from "@/mocks/handlers/siteFiles";
import { useSite } from "@/api/portal/hooks/sites";
import { renderWithQuery, signIn } from "@/test/applicationHarness";
import { SiteDetailPage } from "./SiteDetailPage";
import { SiteFilePage } from "./SiteFilePage";
import { SiteFilesTab } from "./SiteFiles";
import { serveSites } from "./siteTestServer";

// #2590: dev's files in the portal. A save is a release of dev from the one the
// editor read; a newer release that left the file alone takes one click, one
// that changed it is never overwritten; a template's save says how many pages
// change; a failed render names the file and line.

vi.mock("@/components/SourceEditor", () => ({
  SourceEditor: ({ content, onChange }: { content: string; onChange: (v: string) => void }) => (
    <textarea aria-label="Source" value={content} onChange={(e) => onChange(e.target.value)} />
  ),
}));

afterEach(() => vi.unstubAllGlobals());
beforeEach(() => resetSitesMock());

function files() {
  const onNavigate = vi.fn();
  renderWithQuery(<SiteDetailPage site="marketing" tab="files" onNavigate={onNavigate} />);
  return onNavigate;
}

function editor(path: string, fresh = false) {
  const onNavigate = vi.fn();
  renderWithQuery(<SiteFilePage site="marketing" path={path} fresh={fresh} onNavigate={onNavigate} />);
  return onNavigate;
}

async function edit(to: (s: string) => string) {
  const box = await screen.findByRole("textbox", { name: "Source" });
  fireEvent.change(box, { target: { value: to((box as HTMLTextAreaElement).value) } });
}

describe("the Files tab (#2590)", () => {
  it("lists dev's folders and files, and opens a folder", async () => {
    serveSites();
    signIn("admin@example.com", true);
    files();
    expect(await screen.findByTestId("folder-pricing/")).toHaveTextContent("2 files");
    expect(screen.getByTestId("file-index.html")).toBeInTheDocument();
    expect(screen.getByText("dev · release 12")).toBeInTheDocument();
    expect(screen.getByText("Rendered")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("folder-pricing/"));
    expect(await screen.findByTestId("file-pricing/old-plans.html")).toBeInTheDocument();
    expect(screen.queryByTestId("file-index.html")).toBeNull();
  });

  it("filters by path across the whole site", async () => {
    serveSites();
    signIn("admin@example.com", true);
    files();
    fireEvent.change(await screen.findByRole("textbox", { name: "Filter files by path" }), { target: { value: "pricing" } });
    expect(screen.getByTestId("files-count")).toHaveTextContent("2 of 9 files match “pricing”");
  });

  it("opens a file in its editor", async () => {
    serveSites();
    signIn("admin@example.com", true);
    const onNavigate = files();
    fireEvent.click(await screen.findByTestId("file-index.html"));
    expect(onNavigate).toHaveBeenCalledWith("/sites/marketing/file/index.html");
  });

  it("deletes a file as a new release", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    files();
    fireEvent.click(await screen.findByTestId("folder-pricing/"));
    const row = await screen.findByTestId("file-pricing/old-plans.html");
    fireEvent.click(within(row).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete pricing/old-plans.html?" });
    expect(dialog).toHaveTextContent("This makes release 13 without the file");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect(await screen.findByTestId("file-deleted")).toHaveTextContent("Saved as release 13");
    expect(sent.some((r) => r.method === "DELETE" && r.path.includes("path=pricing%2Fold-plans.html&base=12"))).toBe(true);
  });

  it("refuses a new file at a path that is taken", async () => {
    serveSites();
    signIn("admin@example.com", true);
    files();
    fireEvent.click(await screen.findByRole("button", { name: "New file" }));
    const dialog = await screen.findByRole("dialog", { name: "New file" });
    fireEvent.change(within(dialog).getByLabelText("Path"), { target: { value: "pricing/index.html" } });
    expect(within(dialog).getByTestId("new-file-problem")).toHaveTextContent("already exists");
    expect(within(dialog).getByRole("button", { name: "Create" })).toBeDisabled();
  });

  it("is read-only for someone dev is shared with", async () => {
    serveSites();
    signIn("analyst@example.com", false);
    function AsViewer() {
      const { data } = useSite("marketing");
      return data ? <SiteFilesTab site={{ ...data, permissions: { manage: false, own: false, delete: false } }} onNavigate={vi.fn()} /> : null;
    }
    renderWithQuery(<AsViewer />);
    expect(await screen.findByText("You can view this site. People who manage it can change its files.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New file" })).toBeNull();
  });
});

describe("the file editor (#2590)", () => {
  it("saves a change as a release of dev from the one it read", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    editor("pricing/index.html");
    await edit((s) => s.replace("$12", "$15"));
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const dialog = await screen.findByRole("dialog", { name: /Save pricing\/index\.html/ });
    expect(within(dialog).getByTestId("edit-counts")).toHaveTextContent("1 line added, 1 removed");
    expect(dialog).toHaveTextContent("Saving makes release 13 from release 12");
    fireEvent.change(within(dialog).getByLabelText("What changed?"), { target: { value: "Starter price to $15" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(await screen.findByTestId("file-saved")).toHaveTextContent("Saved as release 13");
    expect(sent.find((r) => r.method === "PUT")?.body).toMatchObject({ path: "pricing/index.html", base: 12, note: "Starter price to $15" });
  });

  it("saves on top in one click when dev moved without this file", async () => {
    serveSites();
    signIn("admin@example.com", true);
    editor("pricing/index.html");
    await edit((s) => s.replace("$12", "$15"));
    bumpSiteFile();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save" }));
    const moved = await screen.findByTestId("conflict-moved");
    expect(moved).toHaveTextContent("dev moved to release 13 while you were editing");
    fireEvent.click(within(moved).getByRole("button", { name: "Save on release 13" }));
    expect(await screen.findByTestId("file-saved")).toHaveTextContent("Saved as release 14");
  });

  it("never overwrites a file someone else changed, and loads their version on asking", async () => {
    serveSites();
    signIn("admin@example.com", true);
    editor("pricing/index.html");
    await edit((s) => s.replace("$12", "$15"));
    bumpSiteFile("pricing/index.html", "<h1>Someone else's pricing</h1>\n");
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Save" }));
    const changed = await screen.findByTestId("conflict-changed");
    expect(changed).toHaveTextContent("This file changed in release 13 while you were editing");
    fireEvent.click(within(changed).getByRole("button", { name: "Compare" }));
    expect(await screen.findByTestId("compare")).toHaveTextContent("Your changes · Not saved");
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Load release 13" }));
    await waitFor(() => expect((screen.getByRole("textbox", { name: "Source" }) as HTMLTextAreaElement).value).toBe("<h1>Someone else's pricing</h1>\n"));
  });

  it("says how many pages a layout's save changes", async () => {
    serveSites();
    signIn("admin@example.com", true);
    editor("_layouts/base.html");
    await edit((s) => s.replace("{{ content }}", "{{ content }}<p>Updated</p>"));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByTestId("pages-change")).toHaveTextContent("6,118 pages change when this is saved");
  });

  it("names the file and line when the render fails, and saves nothing", async () => {
    serveSites();
    signIn("admin@example.com", true);
    editor("_layouts/base.html");
    await edit((s) => s.replace("{{ content }}", "{{ page.date | datefmt }}"));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const failed = await screen.findByTestId("render-error");
    expect(failed).toHaveTextContent("_layouts/base.html line 10");
    expect(failed).toHaveTextContent("dev still serves release 12");
  });

  it("does not open a file that is not text", async () => {
    serveSites();
    signIn("admin@example.com", true);
    editor("images/hero.8b1d44f0.webp");
    expect(await screen.findByTestId("file-binary")).toHaveTextContent("This file isn’t text");
  });

  it("asks before leaving with unsaved changes", async () => {
    serveSites();
    signIn("admin@example.com", true);
    const onNavigate = editor("pricing/index.html");
    await edit((s) => `${s}<!-- draft -->`);
    fireEvent.click(screen.getByRole("button", { name: /marketing files/ }));
    const dialog = await screen.findByRole("dialog", { name: "Discard unsaved changes?" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith("/sites/marketing/files"));
  });
});

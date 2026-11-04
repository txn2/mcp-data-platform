import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { renderWithQuery, serveApplications, signIn } from "@/test/applicationHarness";
import { ApplicationDetailPage } from "./ApplicationDetailPage";

// The handlers keep state for the whole file, so the tests that write run
// after the ones that only read, and each write is checked by what it sent.

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function renderPage(app = "app-shop") {
  const onNavigate = vi.fn();
  renderWithQuery(<ApplicationDetailPage app={app} onNavigate={onNavigate} onBack={vi.fn()} />);
  return onNavigate;
}

async function loaded() {
  await screen.findByTestId("part-script-script-001");
}

function lastButton(name: string) {
  const buttons = screen.getAllByRole("button", { name });
  return buttons[buttons.length - 1]!;
}

describe("ApplicationDetailPage", () => {
  it("shows the owner, the current release and each part with its pinned version", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await loaded();

    expect(screen.getByTestId("application-facts")).toHaveTextContent("sarah.chen@example.com");
    expect(screen.getByText("Release 3")).toBeInTheDocument();
    expect(screen.getByTestId("part-script-script-001")).toHaveTextContent("Automation");
    expect(screen.getByTestId("part-script-script-001")).toHaveTextContent("v4");
    expect(screen.getByTestId("part-asset-ast-001")).toHaveTextContent("v3");
    expect(screen.getByTestId("part-source-billing-events")).toHaveTextContent("Webhook source");
  });

  it("lists releases newest first and opens the changes each made", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await loaded();

    const entries = screen.getAllByTestId(/^release-\d+$/);
    expect(entries.map((e) => e.dataset.testid)).toEqual(["release-3", "release-2", "release-1"]);
    expect(entries[0]).toHaveTextContent("Roll back");
    expect(entries[0]).toHaveTextContent("The new report total was wrong.");

    fireEvent.click(within(entries[0]!).getByRole("button", { name: "Changes" }));
    const rollback = screen.getByTestId("changes-3");
    expect(rollback).toHaveTextContent("script-001 v5 → v4");
    expect(rollback).toHaveTextContent("a rollback does not revert the schema");

    fireEvent.click(within(entries[1]!).getByRole("button", { name: "Changes" }));
    const release = screen.getByTestId("changes-2");
    expect(release).toHaveTextContent("ast-001 v2 → v3");
    expect(release).toHaveTextContent("Applied migrations 3");
  });

  it("shows nobody but its owner and administrators the controls", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await loaded();

    for (const name of ["Add part", "Remove", "Release", "Roll back", "Export"]) {
      expect(screen.queryByRole("button", { name }), name).toBeNull();
    }
  });

  it("lets the owner change parts but not release", async () => {
    serveApplications();
    signIn("sarah.chen@example.com", false);
    renderPage();
    await loaded();

    expect(screen.getByRole("button", { name: "Add part" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Remove" })).toHaveLength(4);
    for (const name of ["Release", "Roll back", "Export"]) {
      expect(screen.queryByRole("button", { name }), name).toBeNull();
    }
  });

  it("gives an administrator release, roll back and export, beside what a release would change", async () => {
    serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    await loaded();

    expect(screen.getByRole("button", { name: "Release" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Roll back" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Export" })).toBeEnabled();
    const pending = screen.getByTestId("application-pending");
    expect(pending).toHaveTextContent("script-001 v4 → v5");
    expect(pending).toHaveTextContent("Migrations 4");
  });

  it("shows what stops a release, and holds the button back", async () => {
    serveApplications();
    signIn("admin@example.com", true);
    renderPage("app-intake");
    await screen.findByTestId("part-script-script-004");

    expect(screen.getByTestId("application-pending")).toHaveTextContent(
      "script-004 has no saved version that passed its tests",
    );
    expect(screen.getByRole("button", { name: "Release" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Roll back" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Export" })).toBeDisabled();
    expect(screen.getByText("No releases")).toBeInTheDocument();
  });

  it("downloads the current release as a bundle", async () => {
    const sent = serveApplications();
    signIn("admin@example.com", true);
    const createObjectURL = vi.fn(() => "blob:bundle");
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() }));
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    renderPage();
    await loaded();

    fireEvent.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() => expect(click).toHaveBeenCalled());
    expect(sent.some((r) => r.path === "/api/v1/portal/applications/app-shop/export?release=3")).toBe(true);
    expect(createObjectURL).toHaveBeenCalled();
  });

  it("adds a part through the dialog", async () => {
    const sent = serveApplications();
    signIn("sarah.chen@example.com", false);
    renderPage();
    await loaded();

    fireEvent.click(screen.getByRole("button", { name: "Add part" }));
    fireEvent.change(screen.getByLabelText("ID"), { target: { value: "script-005" } });
    fireEvent.click(lastButton("Add part"));

    expect(await screen.findByTestId("part-script-script-005")).toBeInTheDocument();
    const post = sent.find((r) => r.method === "POST" && r.path.endsWith("/parts"));
    expect(post?.body).toEqual({ kind: "script", id: "script-005" });
  });

  it("says which application already holds a part", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage("app-intake");
    await screen.findByTestId("part-script-script-004");

    fireEvent.click(screen.getByRole("button", { name: "Add part" }));
    fireEvent.change(screen.getByLabelText("ID"), { target: { value: "script-001" } });
    fireEvent.click(lastButton("Add part"));

    expect(await screen.findByText(/already belongs to application "shop"/)).toBeInTheDocument();
  });

  it("removes a part", async () => {
    const sent = serveApplications();
    signIn("sarah.chen@example.com", false);
    renderPage();
    await loaded();

    const row = screen.getByTestId("part-script-script-005");
    fireEvent.click(within(row).getByRole("button", { name: "Remove" }));

    await waitFor(() => expect(screen.queryByTestId("part-script-script-005")).toBeNull());
    expect(
      sent.some((r) => r.method === "DELETE" && r.path === "/api/v1/portal/applications/app-shop/parts/script/script-005"),
    ).toBe(true);
  });

  it("rolls back to the previous release by default", async () => {
    const sent = serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    await loaded();

    fireEvent.click(screen.getByRole("button", { name: "Roll back" }));
    fireEvent.click(lastButton("Roll back"));

    await screen.findByTestId("release-4");
    const post = sent.find((r) => r.path.endsWith("/rollback"));
    expect(post?.body).toEqual({ to_release: 2 });
  });

  it("releases with a note", async () => {
    const sent = serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    await loaded();

    fireEvent.click(screen.getByRole("button", { name: "Release" }));
    fireEvent.change(screen.getByLabelText("Note"), { target: { value: "Refunds again" } });
    fireEvent.click(lastButton("Release"));

    expect(await screen.findByTestId("release-5")).toHaveTextContent("Refunds again");
    const post = sent.find((r) => r.path.endsWith("/release"));
    expect(post?.body).toEqual({ note: "Refunds again" });
  });
});

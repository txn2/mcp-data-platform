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

function openMenu(trigger: HTMLElement) {
  fireEvent.keyDown(trigger, { key: "Enter" });
}

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

function lastButton(name: string) {
  const buttons = screen.getAllByRole("button", { name });
  return buttons[buttons.length - 1]!;
}

describe("ApplicationDetailPage", () => {
  it("shows the owner, the current release and each part by name, grouped by kind, with its pinned version", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await loaded();

    expect(screen.getByTestId("application-facts")).toHaveTextContent("sarah.chen@example.com");
    expect(screen.getByText("Release 3")).toBeInTheDocument();
    expect(screen.getByTestId("parts-subtitle")).toHaveTextContent("5 parts. Release v3 pins the version of each.");
    expect(screen.getByRole("group", { name: "Automations" })).toHaveTextContent("Automations · 2");
    const sales = screen.getByTestId("part-script-script-001");
    expect(sales).toHaveTextContent("Daily sales report");
    expect(sales).toHaveTextContent("Summarizes yesterday's sales by region · every day at 7:00 AM");
    expect(sales).toHaveTextContent("Pinned v4");
    expect(sales).toHaveTextContent("v5 is the latest");
    expect(screen.getByTestId("part-script-script-002")).toHaveTextContent("Pinned v2 · latest");
    const asset = screen.getByTestId("part-asset-ast-001");
    expect(asset).toHaveTextContent("Sales dashboard");
    expect(asset).toHaveTextContent("HTML");
    const source = screen.getByTestId("part-source-billing-events");
    expect(source).toHaveTextContent("1,284 requests in the last day");
    expect(source).toHaveTextContent("Not versioned");
    expect(screen.getByTestId("parts-behind")).toHaveTextContent(
      "1 part has a newer version than release v3 pins. Publish a release to pin it.",
    );
  });

  it("never shows an id in place of a name, and names a part that is gone", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await loaded();

    const card = screen.getByTestId("application-parts");
    expect(card.textContent).not.toMatch(UUID);
    for (const id of ["script-001", "script-002", "ast-001"]) expect(card).not.toHaveTextContent(id);
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

    for (const name of ["Add parts", "Release", "Roll back", "Export"]) {
      expect(screen.queryByRole("button", { name }), name).toBeNull();
    }
    openMenu(screen.getByRole("button", { name: "Actions for Daily sales report" }));
    expect(screen.getByRole("menuitem", { name: "Copy reference" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove from application" })).toBeNull();
  });

  it("lets the owner change parts but not release", async () => {
    serveApplications();
    signIn("sarah.chen@example.com", false);
    renderPage();
    await loaded();

    expect(screen.getByRole("button", { name: "Add parts" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Actions for / })).toHaveLength(5);
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

  it("adds two parts picked by name in one go", async () => {
    const sent = serveApplications();
    signIn("sarah.chen@example.com", false);
    renderPage();
    await loaded();

    fireEvent.click(screen.getByRole("button", { name: "Add parts" }));
    expect(screen.getByRole("heading", { name: "Add parts to shop" })).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText("Search automations, assets and webhook sources"), {
      target: { value: "revenue" },
    });
    fireEvent.click(await screen.findByRole("checkbox", { name: "Monthly revenue close" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Revenue by region" }));
    expect(screen.getByText("2 selected")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add 2 parts" }));

    expect(await screen.findByTestId("part-script-script-005")).toHaveTextContent("Monthly revenue close");
    expect(await screen.findByTestId("part-asset-ast-002")).toHaveTextContent("Revenue by region");
    const posts = sent.filter((r) => r.method === "POST" && r.path.endsWith("/parts")).map((r) => r.body);
    expect(posts).toEqual([
      { kind: "script", id: "script-005" },
      { kind: "asset", id: "ast-002" },
    ]);
  });

  it("disables a part another application holds and names it", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage("app-intake");
    await screen.findByTestId("part-script-script-004");

    fireEvent.click(screen.getByRole("button", { name: "Add parts" }));
    const taken = await screen.findByRole("checkbox", { name: "Daily sales report, already in shop" });
    expect(taken).toBeDisabled();
    expect(screen.getByTestId("candidate-script-script-001")).toHaveTextContent(
      "Already part of shop. Remove it there to add it here.",
    );
    expect(screen.getByRole("button", { name: "Add 0 parts" })).toBeDisabled();
  });

  it("removes a part after asking", async () => {
    const sent = serveApplications();
    signIn("sarah.chen@example.com", false);
    renderPage();
    await loaded();

    openMenu(screen.getByRole("button", { name: "Actions for Monthly revenue close" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Remove from application" }));
    expect(
      screen.getByText("Remove Monthly revenue close from shop? It stays in the portal; the next release no longer pins it."),
    ).toBeInTheDocument();
    expect(sent.some((r) => r.method === "DELETE")).toBe(false);
    fireEvent.click(lastButton("Remove"));

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

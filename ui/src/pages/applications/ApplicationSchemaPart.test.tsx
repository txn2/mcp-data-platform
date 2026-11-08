import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { renderWithQuery, serveApplications, signIn } from "@/test/applicationHarness";
import { ApplicationDetailPage } from "./ApplicationDetailPage";

// The schema part (#2539): an application's database schema is one of its
// parts, shown with its migration version and what its run role still needs,
// added from the Add parts dialog's Schema view and removed like any part.
// The handlers keep state for the whole file, so the tests that read shop's
// schema run before the one that removes it.

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const SHOP_SCHEMA = "part-schema-acme-shop/shop";

function renderPage(app: string) {
  renderWithQuery(<ApplicationDetailPage app={app} onNavigate={vi.fn()} onBack={vi.fn()} />);
}

function openMenu(trigger: HTMLElement) {
  fireEvent.keyDown(trigger, { key: "Enter" });
}

function lastButton(name: string) {
  const buttons = screen.getAllByRole("button", { name });
  return buttons[buttons.length - 1]!;
}

async function openSchemaView(app: string) {
  renderPage(app);
  await screen.findByTestId(app === "app-shop" ? SHOP_SCHEMA : "part-script-script-004");
  fireEvent.click(screen.getByRole("button", { name: "Add parts" }));
  fireEvent.click(screen.getByRole("tab", { name: "Schema" }));
}

describe("Application schema part", () => {
  it("lists the schema last, under Schema, with its migration, pending count and run role note", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage("app-shop");

    const row = await screen.findByTestId(SHOP_SCHEMA);
    const group = screen.getByRole("group", { name: "Schema" });
    expect(group).toHaveTextContent("Schema · 1");
    expect(within(group).getByTestId(SHOP_SCHEMA)).toBe(row);
    const groups = screen.getAllByRole("group").map((g) => g.getAttribute("aria-label"));
    expect(groups[groups.length - 1]).toBe("Schema");

    expect(row).toHaveTextContent("acme-shop/shop");
    expect(row).toHaveTextContent("migration 3 applied · release v3 recorded 2 · 1 pending · scripts run as shop_run");
    expect(row).toHaveTextContent("Migration 3");
    expect(row).toHaveTextContent("1 pending");
    expect(within(row).getByTestId("schema-note")).toHaveTextContent(
      "the application's scripts log in as shop_run, which reaches no table until a migration defines shop_write",
    );
    // The schema's pending migration is the release's, not a part behind its pin.
    expect(screen.getByTestId("parts-behind")).toHaveTextContent("1 part has a newer version");
    // A reader who cannot edit gets no menu on it: it has no page and no reference.
    expect(screen.queryByRole("button", { name: "Actions for acme-shop/shop" })).toBeNull();
  });

  it("says which schema an application already holds and offers nothing to add", async () => {
    serveApplications();
    signIn("sarah.chen@example.com", false);
    await openSchemaView("app-shop");

    expect(screen.getByTestId("schema-choices")).toHaveTextContent("This application holds schema acme-shop/shop.");
    expect(screen.queryByRole("button", { name: "Add schema" })).toBeNull();
  });

  it("shows a schema another application holds as held, and the server's refusal of it verbatim", async () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    await openSchemaView("app-intake");

    const shop = await screen.findByTestId("schema-connection-acme-shop");
    expect(within(shop).getByRole("radio", { name: "shop, held by shop" })).toBeDisabled();
    expect(shop).toHaveTextContent("Held by shop");

    fireEvent.change(within(shop).getByLabelText("New schema name"), { target: { value: "shop" } });
    fireEvent.click(screen.getByRole("button", { name: "Add schema" }));

    expect(await screen.findByText('schema acme-shop/shop belongs to application "shop"')).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Add parts to intake" })).toBeInTheDocument();
    const posts = sent.filter((r) => r.method === "POST" && r.path.endsWith("/parts")).map((r) => r.body);
    expect(posts).toEqual([{ kind: "schema", id: "acme-shop/shop" }]);
  });

  it("adds a schema picked from a connection, and one typed under it", async () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    await openSchemaView("app-intake");

    const inventory = await screen.findByTestId("schema-connection-inventory");
    fireEvent.click(within(inventory).getByRole("radio", { name: "public" }));
    const shop = screen.getByTestId("schema-connection-acme-shop");
    fireEvent.change(within(shop).getByLabelText("New schema name"), { target: { value: " intake " } });
    fireEvent.click(screen.getByRole("button", { name: "Add schema" }));

    const row = await screen.findByTestId("part-schema-acme-shop/intake");
    expect(row).toHaveTextContent("no migrations applied · scripts run as intake_run");
    expect(row).toHaveTextContent("No migrations");
    const posts = sent.filter((r) => r.method === "POST" && r.path.endsWith("/parts")).map((r) => r.body);
    expect(posts[posts.length - 1]).toEqual({ kind: "schema", id: "acme-shop/intake" });
  });

  it("removes the schema after saying what is dropped, with its id escaped in the path", async () => {
    const sent = serveApplications();
    signIn("sarah.chen@example.com", false);
    renderPage("app-shop");
    await screen.findByTestId(SHOP_SCHEMA);

    openMenu(screen.getByRole("button", { name: "Actions for acme-shop/shop" }));
    expect(screen.queryByRole("menuitem", { name: "Copy reference" })).toBeNull();
    fireEvent.click(screen.getByRole("menuitem", { name: "Remove from application" }));
    expect(
      screen.getByText(
        "Remove schema acme-shop/shop from shop? The database role its automations log in as is dropped; the schema and its data stay.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(lastButton("Remove"));

    await waitFor(() => expect(screen.queryByTestId(SHOP_SCHEMA)).toBeNull());
    expect(
      sent.some(
        (r) => r.method === "DELETE" && r.path === "/api/v1/portal/applications/app-shop/parts/schema/acme-shop%2Fshop",
      ),
    ).toBe(true);
  });
});

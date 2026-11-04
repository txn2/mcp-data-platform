import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { mockApplicationBundle } from "@/mocks/data/applications";
import { renderWithQuery, serveApplications, signIn } from "@/test/applicationHarness";
import { ApplicationsPage } from "./ApplicationsPage";

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderPage() {
  const onNavigate = vi.fn();
  renderWithQuery(<ApplicationsPage onNavigate={onNavigate} />);
  return onNavigate;
}

describe("ApplicationsPage", () => {
  it("lists every application with its owner and current release", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();

    const shop = await screen.findByTestId("application-row-app-shop");
    expect(shop).toHaveTextContent("shop");
    expect(shop).toHaveTextContent("sarah.chen@example.com");
    expect(shop).toHaveTextContent("3");
    expect(screen.getByTestId("application-row-app-intake")).toHaveTextContent("None");
  });

  it("opens an application from its row", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    const onNavigate = renderPage();

    fireEvent.click(await screen.findByTestId("application-row-app-shop"));
    expect(onNavigate).toHaveBeenCalledWith("/applications/app-shop");
  });

  it("offers Import to administrators only", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    const { unmount } = renderWithQuery(<ApplicationsPage onNavigate={vi.fn()} />);
    await screen.findByTestId("application-row-app-shop");
    expect(screen.getByRole("button", { name: "New application" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Import" })).toBeNull();
    unmount();

    signIn("admin@example.com", true);
    renderPage();
    await screen.findByTestId("application-row-app-shop");
    expect(screen.getByRole("button", { name: "Import" })).toBeInTheDocument();
  });

  it("creates an application from the dialog and opens it", async () => {
    const sent = serveApplications();
    signIn("j.martinez@example.com", false);
    const onNavigate = renderPage();
    await screen.findByTestId("application-row-app-shop");

    fireEvent.click(screen.getByRole("button", { name: "New application" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ledger" } });
    fireEvent.change(screen.getByLabelText("Description"), { target: { value: "The books" } });
    fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "acme-shop" } });
    fireEvent.change(screen.getByLabelText("Schema"), { target: { value: "ledger" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith("/applications/app-ledger"));
    const post = sent.find((r) => r.method === "POST" && r.path === "/api/v1/portal/applications");
    expect(post?.body).toEqual({
      name: "ledger",
      description: "The books",
      connection: "acme-shop",
      schema: "ledger",
    });
  });

  it("shows the server's refusal when the name is taken", async () => {
    serveApplications();
    signIn("j.martinez@example.com", false);
    renderPage();
    await screen.findByTestId("application-row-app-shop");

    fireEvent.click(screen.getByRole("button", { name: "New application" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "shop" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    expect(await screen.findByText(/already exists/)).toBeInTheDocument();
  });

  it("imports a bundle with one name field per name it refers to", async () => {
    const sent = serveApplications();
    signIn("admin@example.com", true);
    const onNavigate = renderPage();
    await screen.findByTestId("application-row-app-shop");

    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    const file = new File([JSON.stringify(mockApplicationBundle)], "shop.json", {
      type: "application/json",
    });
    fireEvent.change(screen.getByLabelText("Bundle"), { target: { files: [file] } });

    // Every name, prefilled with itself.
    const connection = (await screen.findByLabelText("acme-shop")) as HTMLInputElement;
    expect(connection.value).toBe("acme-shop");
    expect((screen.getByLabelText("stripe-key") as HTMLInputElement).value).toBe("stripe-key");
    expect((screen.getByLabelText("billing-events") as HTMLInputElement).value).toBe(
      "billing-events",
    );

    fireEvent.change(connection, { target: { value: "orders" } });
    const buttons = screen.getAllByRole("button", { name: "Import" });
    fireEvent.click(buttons[buttons.length - 1]!);

    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith("/applications/shop"));
    const post = sent.find((r) => r.path === "/api/v1/portal/applications/import");
    expect(post?.body).toEqual({
      bundle: mockApplicationBundle,
      map: { "acme-shop": "orders", "billing-events": "billing-events", "stripe-key": "stripe-key" },
    });
  });

  it("refuses a file that is not a bundle", async () => {
    serveApplications();
    signIn("admin@example.com", true);
    renderPage();
    await screen.findByTestId("application-row-app-shop");

    fireEvent.click(screen.getByRole("button", { name: "Import" }));
    const file = new File(["not json"], "x.json", { type: "application/json" });
    fireEvent.change(screen.getByLabelText("Bundle"), { target: { files: [file] } });

    expect(await screen.findByText("This file is not an application bundle.")).toBeInTheDocument();
  });
});

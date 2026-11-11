import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { getResponse } from "msw";
import { handlers } from "@/mocks/handlers";
import { renderWithQuery, signIn, type SentRequest } from "@/test/applicationHarness";
import { SitesSettingsCard } from "./SitesSettingsCard";

// #2602 in Admin > Settings > Sites: the releases each environment keeps by
// default, what the daily cleanup does, and what the last one removed.

let sent: SentRequest[] = [];

beforeEach(() => {
  sent = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, window.location.origin);
      sent.push({ method: init?.method ?? "GET", path: url.pathname, body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined });
      const res = await getResponse(handlers, new Request(url, init));
      return res ?? new Response("{}", { status: 404 });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

describe("Sites settings: releases kept (#2602)", () => {
  it("reads and saves the default and the cleanup mode", async () => {
    signIn("admin@example.com", true);
    renderWithQuery(<SitesSettingsCard />);
    const releases = await screen.findByTestId("sites-releases");
    const keep = within(releases).getByRole("spinbutton");
    expect(keep).toHaveValue(50);
    expect(within(releases).getByRole("radio", { name: "Delete" })).toBeChecked();
    expect(await within(releases).findByTestId("last-cleanup")).toHaveTextContent("Removed 1,284 releases from 23 sites");
    fireEvent.change(keep, { target: { value: "20" } });
    fireEvent.click(within(releases).getByRole("radio", { name: "Report only" }));
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT")?.body).toMatchObject({ releases_keep: 20, releases_mode: "report" }));
  });

  it("does not save an emptied number as keep every release", async () => {
    signIn("admin@example.com", true);
    renderWithQuery(<SitesSettingsCard />);
    const releases = await screen.findByTestId("sites-releases");
    const keep = within(releases).getByRole("spinbutton");
    await waitFor(() => expect(keep).not.toHaveValue(null));
    fireEvent.change(keep, { target: { value: "" } });
    expect(within(releases).getByText("Enter a whole number. 0 keeps every release.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    fireEvent.change(keep, { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    await waitFor(() => expect(sent.find((r) => r.method === "PUT")?.body).toMatchObject({ releases_keep: 0 }));
    expect(sent.filter((r) => r.method === "PUT")).toHaveLength(1);
  });
});

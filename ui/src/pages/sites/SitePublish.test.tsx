import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { screen, fireEvent, waitFor, within } from "@testing-library/react";
import { advanceSiteJob, endSiteJob, resetSitesMock } from "@/mocks/handlers/sites";
import { renderWithQuery, signIn } from "@/test/applicationHarness";
import { SiteDetailPage } from "./SiteDetailPage";
import { serveSites } from "./siteTestServer";

// #2593: Publish to dev from an upload, an archive in Resources or a bucket
// prefix on an S3 connection the person can use. It runs as a job: four phases
// while it runs, a banner with Stop once the dialog is closed, and how it
// ended, a refusal in the server's own words.

afterEach(() => vi.unstubAllGlobals());
beforeEach(() => resetSitesMock());

const POLL = { timeout: 4000 };

async function openPublish() {
  renderWithQuery(<SiteDetailPage site="marketing" tab="environments" onNavigate={vi.fn()} />);
  fireEvent.click(within(await screen.findByTestId("env-dev")).getByRole("button", { name: "Publish" }));
  return screen.findByRole("dialog", { name: "Publish to dev" });
}

async function fromBucket(dialog: HTMLElement, connection: string) {
  fireEvent.mouseDown(within(dialog).getByRole("tab", { name: "Bucket prefix" }));
  fireEvent.click(within(dialog).getByRole("tab", { name: "Bucket prefix" }));
  const picker = await within(dialog).findByLabelText("Connection");
  await waitFor(() => expect(within(picker).getByRole("option", { name: `${connection} · S3` })).toBeInTheDocument());
  fireEvent.change(picker, { target: { value: connection } });
  fireEvent.change(within(dialog).getByLabelText("Bucket"), { target: { value: "acme-web-source" } });
  fireEvent.change(within(dialog).getByLabelText("Prefix"), { target: { value: "cool-site/build/" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Publish to dev" }));
}

function jobIdOf(sent: { method: string; path: string }[]) {
  return sent.some((r) => r.method === "POST" && r.path.endsWith("/publish")) ? "sjob_1" : "";
}

describe("Publish to dev (#2593)", () => {
  it("offers Upload, Archive in Resources and Bucket prefix", async () => {
    serveSites();
    signIn("admin@example.com", true);
    const dialog = await openPublish();
    expect(within(dialog).getByRole("tab", { name: "Archive in Resources" })).toBeInTheDocument();
    expect(within(dialog).getByRole("tab", { name: "Bucket prefix" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("tab", { name: "From Resources" })).toBeNull();
  });

  it("lists only S3 connections and publishes from the prefix, showing the four phases", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    const dialog = await openPublish();
    fireEvent.mouseDown(within(dialog).getByRole("tab", { name: "Bucket prefix" }));
    fireEvent.click(within(dialog).getByRole("tab", { name: "Bucket prefix" }));
    const picker = await within(dialog).findByLabelText("Connection");
    await waitFor(() => expect(within(picker).getAllByRole("option")).toHaveLength(3));
    expect(within(picker).queryByRole("option", { name: /acme-warehouse/ })).toBeNull();
    await fromBucket(dialog, "web-source");

    await waitFor(() =>
      expect(sent.find((r) => r.method === "POST" && r.path.endsWith("/publish"))?.body).toEqual({
        note: "",
        from_bucket: { connection: "web-source", bucket: "acme-web-source", prefix: "cool-site/build/" },
      }),
    );
    const running = await screen.findByRole("dialog", { name: "Publishing to dev" });
    const phases = await within(running).findByTestId("publish-phases");
    expect(within(phases).getByTestId("publish-phase-0")).toHaveTextContent("Reading filesDone");
    expect(within(phases).getByTestId("publish-phase-1")).toHaveTextContent("3,020 of 5,214");
    expect(within(phases).getByTestId("publish-phase-2")).toHaveTextContent("Rendering pagesWaiting");
    expect(within(phases).getByTestId("publish-phase-3")).toHaveTextContent("Switching dev to release 13");
    expect(running).toHaveTextContent("Closing this window doesn’t stop it.");
  });

  it("shows the connection's refusal in the server's words, and goes back to the form", async () => {
    serveSites();
    signIn("admin@example.com", true);
    await fromBucket(await openPublish(), "locked-s3");
    const refused = await screen.findByRole("dialog", { name: "Nothing was published" });
    expect(within(refused).getByTestId("publish-refused")).toHaveTextContent("locked-s3 refused to list acme-web-source/cool-site/build/");
    expect(refused).toHaveTextContent("dev still serves release 12");
    fireEvent.click(within(refused).getByRole("button", { name: "Choose another source" }));
    expect(await screen.findByRole("dialog", { name: "Publish to dev" })).toBeInTheDocument();
  });

  it("says what was published once the job is done", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    await fromBucket(await openPublish(), "web-source");
    await screen.findByRole("dialog", { name: "Publishing to dev" });
    endSiteJob(jobIdOf(sent), "done");
    const done = await screen.findByRole("dialog", { name: "Published to dev" }, POLL);
    expect(within(done).getByTestId("publish-done")).toHaveTextContent("dev serves release 13");
    expect(done).toHaveTextContent("6,118 pages rendered");
    expect(within(done).getByRole("button", { name: "Promote" })).toBeInTheDocument();
  });

  it("shows a job's refusal: the file it could not serve", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    await fromBucket(await openPublish(), "web-source");
    await screen.findByRole("dialog", { name: "Publishing to dev" });
    endSiteJob(jobIdOf(sent), "failed", "nothing was published: 2 files can't be served: .env and .git/HEAD have a segment starting with a dot");
    const refused = await screen.findByRole("dialog", { name: "Nothing was published" }, POLL);
    expect(within(refused).getByTestId("publish-refused")).toHaveTextContent(".env and .git/HEAD");
  });

  it("stops a running publish", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    await fromBucket(await openPublish(), "web-source");
    const running = await screen.findByRole("dialog", { name: "Publishing to dev" });
    fireEvent.click(await within(running).findByRole("button", { name: "Stop" }));
    await waitFor(() => expect(sent.some((r) => r.method === "POST" && r.path.endsWith("/jobs/sjob_1/stop"))).toBe(true));
    const stopped = await screen.findByRole("dialog", { name: "Publish stopped" }, POLL);
    expect(within(stopped).getByTestId("publish-stopped")).toHaveTextContent("dev still serves release 12");
  });

  it("shows a running publish on the Environments tab with Stop once its dialog is closed", async () => {
    const sent = serveSites();
    signIn("admin@example.com", true);
    await fromBucket(await openPublish(), "web-source");
    const running = await screen.findByRole("dialog", { name: "Publishing to dev" });
    advanceSiteJob(jobIdOf(sent), "Rendering", 4000);
    fireEvent.click(within(running).getByText("Close", { selector: "button" }));
    const banner = await screen.findByTestId("publish-banner", {}, POLL);
    expect(banner).toHaveTextContent("Publishing to dev from s3://acme-web-source/cool-site/build/ on connection web-source");
    await waitFor(() => expect(banner).toHaveTextContent("Rendering pages: 4,000 of 5,214"), POLL);
    fireEvent.click(within(banner).getByRole("button", { name: "Show progress" }));
    const again = await screen.findByRole("dialog", { name: "Publishing to dev" });
    fireEvent.click(within(again).getByText("Close", { selector: "button" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    fireEvent.click(within(screen.getByTestId("publish-banner")).getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(sent.some((r) => r.method === "POST" && r.path.endsWith("/stop"))).toBe(true));
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import {
  DownloadButton,
  NOTICES_HEADER,
  isStandaloneType,
  readNotices,
  standaloneFileName,
  standaloneUrlFor,
  storedSourceUrlFor,
} from "./StandaloneDownload";

describe("standalone download URLs", () => {
  it("puts the standalone route beside each content route a page downloads from", () => {
    expect(standaloneUrlFor("/api/v1/portal/assets/a1/content")).toBe("/api/v1/portal/assets/a1/standalone");
    expect(standaloneUrlFor("/portal/view/tok/content")).toBe("/portal/view/tok/standalone");
    expect(standaloneUrlFor("/portal/view/tok/items/a1/content")).toBe("/portal/view/tok/items/a1/standalone");
    expect(standaloneUrlFor("/api/v1/portal/assets/a1/content?x=1")).toBe("/api/v1/portal/assets/a1/standalone");
  });

  it("has none for a version or a signed URL, which download as stored", () => {
    expect(standaloneUrlFor("/api/v1/portal/assets/a1/versions/3/content")).toBeNull();
    expect(standaloneUrlFor("/api/v1/portal/content/abc?sig=x")).toBeNull();
    expect(standaloneUrlFor("/api/v1/admin/assets/a1/content")).toBeNull();
  });

  it("asks the content route for the source as stored", () => {
    expect(storedSourceUrlFor("/portal/view/tok/content")).toBe("/portal/view/tok/content?source=stored");
    expect(storedSourceUrlFor("/a/content?x=1")).toBe("/a/content?x=1&source=stored");
  });

  it("names the file for the asset, with .html", () => {
    expect(standaloneFileName("Quarterly deck.jsx")).toBe("Quarterly deck.html");
    expect(standaloneFileName("deck")).toBe("deck.html");
  });

  it("offers it for HTML and JSX alone", () => {
    expect(isStandaloneType("text/html")).toBe(true);
    expect(isStandaloneType("text/jsx")).toBe(true);
    expect(isStandaloneType("text/csv")).toBe(false);
    expect(isStandaloneType("application/pdf")).toBe(false);
  });

  it("reads the notices header and ignores what it cannot read", () => {
    const header = encodeURIComponent(JSON.stringify([{ url: "https://cdn.example.org/a.js", reason: "could not be fetched" }]));
    expect(readNotices(header)).toEqual([{ url: "https://cdn.example.org/a.js", reason: "could not be fetched" }]);
    expect(readNotices(null)).toEqual([]);
    expect(readNotices("%%%")).toEqual([]);
    expect(readNotices(encodeURIComponent('{"url":"x"}'))).toEqual([]);
  });
});

describe("DownloadButton", () => {
  const fetchMock = vi.fn();
  const clicked: string[] = [];

  beforeEach(() => {
    fetchMock.mockReset();
    clicked.length = 0;
    vi.stubGlobal("fetch", fetchMock);
    URL.createObjectURL = vi.fn(() => "blob:x");
    URL.revokeObjectURL = vi.fn();
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.download);
    });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("is a plain link for any other type, as before", () => {
    render(<DownloadButton contentUrl="/api/v1/portal/assets/a1/content" name="data.csv" contentType="text/csv" />);
    const link = screen.getByRole("link", { name: /download/i });
    expect(link.getAttribute("href")).toBe("/api/v1/portal/assets/a1/content");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("saves the standalone file with no dialog when nothing was left out", async () => {
    fetchMock.mockResolvedValue(new Response("<html></html>", { status: 200 }));
    render(<DownloadButton contentUrl="/api/v1/portal/assets/a1/content" name="deck.html" contentType="text/html" />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /download/i })));
    await waitFor(() => expect(clicked).toEqual(["deck.html"]));
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/portal/assets/a1/standalone", { credentials: "include", headers: {} });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("names the files left as URLs after saving", async () => {
    const notices = encodeURIComponent(JSON.stringify([{ url: "http://127.0.0.1/x.png", reason: "could not be fetched: refused" }]));
    fetchMock.mockResolvedValue(new Response("<html></html>", { status: 200, headers: { [NOTICES_HEADER]: notices } }));
    render(<DownloadButton contentUrl="/portal/view/tok/content" name="dash.jsx" contentType="text/jsx" />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /download/i })));
    expect(await screen.findByText("Downloaded")).toBeTruthy();
    expect(screen.getByText(/127\.0\.0\.1\/x\.png/)).toBeTruthy();
    expect(clicked).toEqual(["dash.html"]);
    expect(screen.queryByRole("button", { name: /download source/i })).toBeNull();
  });

  it("offers the source as stored when the asset reads platform data, and Cancel saves nothing", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "platform_data", reasons: ["it embeds a form, which submits to the platform"] }), { status: 409 }),
    );
    render(<DownloadButton contentUrl="/api/v1/portal/assets/a1/content" name="survey.html" contentType="text/html" />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /download/i })));
    expect(await screen.findByText("This asset reads data from the platform")).toBeTruthy();
    expect(screen.getByText(/embeds a form/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(clicked).toEqual([]);

    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ reasons: ["x"] }), { status: 409 }));
    fetchMock.mockResolvedValueOnce(new Response("<html>stored</html>", { status: 200 }));
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /download/i })));
    await act(async () => fireEvent.click(await screen.findByRole("button", { name: /download source/i })));
    await waitFor(() => expect(clicked).toEqual(["survey.html"]));
    expect(fetchMock).toHaveBeenLastCalledWith("/api/v1/portal/assets/a1/content?source=stored", { credentials: "include", headers: {} });
  });

  it("says why a JSX asset did not build", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ error: "build_failed", reasons: ['imports "lodash"'] }), { status: 422 }));
    render(<DownloadButton contentUrl="/api/v1/portal/assets/a1/content" name="a.jsx" contentType="text/jsx" />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /download/i })));
    expect(await screen.findByText("Could not make a standalone file")).toBeTruthy();
    expect(screen.getByText(/lodash/)).toBeTruthy();
  });

  it("sends the page's own read headers", async () => {
    fetchMock.mockResolvedValue(new Response("<html></html>", { status: 200 }));
    render(
      <DownloadButton
        contentUrl="/api/v1/portal/assets/a1/content"
        name="deck.html"
        contentType="text/html"
        authHeaders={() => ({ "X-API-Key": "k" })}
      />,
    );
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /download/i })));
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/portal/assets/a1/standalone", { credentials: "include", headers: { "X-API-Key": "k" } });
  });

  it("says when the platform could not be reached", async () => {
    fetchMock.mockRejectedValue(new Error("offline"));
    render(<DownloadButton contentUrl="/api/v1/portal/assets/a1/content" name="a.html" contentType="text/html" />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: /download/i })));
    expect(await screen.findByText(/could not reach the platform/)).toBeTruthy();
  });
});

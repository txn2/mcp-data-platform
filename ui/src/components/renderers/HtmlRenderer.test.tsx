import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { HtmlRenderer } from "./HtmlRenderer";

const DECK = `<!DOCTYPE html><html><head><script src="/portal/vendor/reveal/reveal.js"></script></head><body><div class="reveal"><div class="slides"><section>One</section></div></div></body></html>`;
const DASHBOARD = `<!DOCTYPE html><html><head><title>Revenue</title></head><body><h1>Revenue</h1></body></html>`;

afterEach(() => {
  vi.restoreAllMocks();
});

/** jsdom has no fullscreen API; this stands one up for a test that needs it. */
function enableFullscreen(): void {
  Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: true });
}

/** The presented frame: the one without the modals grant. */
function presentedFrame(container: HTMLElement): HTMLIFrameElement {
  return container.querySelector('iframe[sandbox="allow-scripts"]')!;
}

describe("HtmlRenderer", () => {
  it("frames the document as srcdoc, sandboxed without a same-origin grant, with fullscreen permitted", () => {
    const { container } = render(<HtmlRenderer content={DECK} />);
    const frame = container.querySelector("iframe");
    expect(frame).not.toBeNull();
    // srcdoc, not a blob: URL, so a root-relative script path in the artifact
    // resolves against the page (#1767).
    expect(frame).toHaveAttribute("srcdoc", DECK);
    expect(frame).not.toHaveAttribute("src");
    expect(frame).toHaveAttribute("sandbox", "allow-scripts");
    expect(frame).toHaveAttribute("allow", "fullscreen");
  });

  it("offers no Present control where the browser cannot fullscreen", () => {
    Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: false });
    render(<HtmlRenderer content={DECK} />);
    expect(screen.queryByRole("button", { name: /present/i })).toBeNull();
  });

  it("Present fullscreens the frame and moves focus into it", () => {
    enableFullscreen();
    const requestFullscreen = vi.fn(() => Promise.resolve());
    HTMLIFrameElement.prototype.requestFullscreen = requestFullscreen;
    const { container } = render(<HtmlRenderer content={DECK} />);
    const frame = presentedFrame(container);
    const focus = vi.spyOn(frame, "focus");

    fireEvent.click(screen.getByRole("button", { name: /present/i }));

    expect(requestFullscreen).toHaveBeenCalledTimes(1);
    expect(focus).toHaveBeenCalledTimes(1);
  });

  it("a refused fullscreen request still hands the keyboard to the frame", async () => {
    enableFullscreen();
    HTMLIFrameElement.prototype.requestFullscreen = vi.fn(() => Promise.reject(new Error("denied")));
    const { container } = render(<HtmlRenderer content={DECK} />);
    const frame = presentedFrame(container);
    const focus = vi.spyOn(frame, "focus");

    fireEvent.click(screen.getByRole("button", { name: /present/i }));
    await Promise.resolve();

    expect(focus).toHaveBeenCalledTimes(1);
  });

  it("renders the controls in the page's slot when it has one, above the frame otherwise (#1769)", () => {
    enableFullscreen();
    const slot = document.createElement("div");
    document.body.appendChild(slot);
    const { container, unmount } = render(<HtmlRenderer content={DECK} controlsSlot={slot} />);
    expect(slot.querySelector("button")).not.toBeNull();
    expect(container.querySelector("button")).toBeNull();
    unmount();
    slot.remove();

    const inline = render(<HtmlRenderer content={DECK} pdfUrl="/api/v1/portal/assets/a1/pdf" />);
    const buttons = inline.container.querySelectorAll("button");
    expect(buttons.length).toBe(3);
    // The controls come before the frame in the document, so a reader tabbing
    // through the page reaches them first.
    expect(buttons[0]!.compareDocumentPosition(presentedFrame(inline.container)) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("offers Overview only for a document on the served runtime, and asks the runtime for it by message", () => {
    const { container, unmount } = render(<HtmlRenderer content={DASHBOARD} />);
    expect(screen.queryByRole("button", { name: /overview/i })).toBeNull();
    unmount();

    const deck = render(<HtmlRenderer content={DECK} />);
    const frame = presentedFrame(deck.container);
    const postMessage = vi.spyOn(frame.contentWindow!, "postMessage");
    const focus = vi.spyOn(frame, "focus");

    fireEvent.click(screen.getByRole("button", { name: /overview/i }));

    expect(postMessage).toHaveBeenCalledWith(JSON.stringify({ method: "toggleOverview", args: [] }), "*");
    expect(focus).toHaveBeenCalledTimes(1);
    expect(container.querySelector("iframe")).toBeNull();
  });

  it("Export PDF downloads the document's PDF from the route beside its content (#1983)", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(new Blob(["%PDF-1.7"]), {
        status: 200,
        headers: { "Content-Type": "application/pdf", "Content-Disposition": 'inline; filename="Q3 review.pdf"' },
      }),
    );
    const createURL = vi.fn(() => "blob:pdf");
    URL.createObjectURL = createURL;
    URL.revokeObjectURL = vi.fn();
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);

    render(<HtmlRenderer content={DECK} pdfUrl="/api/v1/portal/assets/a1/pdf" />);
    const button = screen.getByRole("button", { name: /export pdf/i });
    expect(button).toHaveAttribute("title", expect.stringMatching(/one page per slide/));
    await act(async () => {
      fireEvent.click(button);
    });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/portal/assets/a1/pdf", { credentials: "same-origin" });
    expect(createURL).toHaveBeenCalledTimes(1);
    expect(click).toHaveBeenCalledTimes(1);
    const anchor = click.mock.contexts[0] as HTMLAnchorElement;
    expect(anchor.download).toBe("Q3 review.pdf");
    expect(document.querySelector("iframe[sandbox*='allow-modals']")).toBeNull();
    expect(screen.getByRole("button", { name: /export pdf/i })).toBeEnabled();
  });

  it("says why when the PDF could not be made", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response("The PDF renderer is not available. Try again shortly.", { status: 503 }),
    );
    render(<HtmlRenderer content={DASHBOARD} pdfUrl="/portal/view/tok/pdf" />);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /export pdf/i }));
    });
    expect(screen.getByRole("alert")).toHaveTextContent("The PDF renderer is not available");
  });

  it("offers no Export PDF without a PDF route", () => {
    render(<HtmlRenderer content={DECK} />);
    expect(screen.queryByRole("button", { name: /export pdf/i })).toBeNull();
  });
});

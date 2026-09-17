import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { HtmlRenderer } from "./HtmlRenderer";

const DECK = `<!DOCTYPE html><html><head><script src="/portal/vendor/reveal/reveal.js"></script></head><body><div class="reveal"><div class="slides"><section>One</section></div></div></body></html>`;

afterEach(() => {
  vi.restoreAllMocks();
});

/** jsdom has no fullscreen API; this stands one up for a test that needs it. */
function enableFullscreen(): void {
  Object.defineProperty(document, "fullscreenEnabled", { configurable: true, value: true });
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
    const frame = container.querySelector("iframe")!;
    const focus = vi.spyOn(frame, "focus");

    fireEvent.click(screen.getByRole("button", { name: /present/i }));

    expect(requestFullscreen).toHaveBeenCalledTimes(1);
    expect(focus).toHaveBeenCalledTimes(1);
  });

  it("a refused fullscreen request still hands the keyboard to the frame", async () => {
    enableFullscreen();
    HTMLIFrameElement.prototype.requestFullscreen = vi.fn(() => Promise.reject(new Error("denied")));
    const { container } = render(<HtmlRenderer content={DECK} />);
    const frame = container.querySelector("iframe")!;
    const focus = vi.spyOn(frame, "focus");

    fireEvent.click(screen.getByRole("button", { name: /present/i }));
    await Promise.resolve();

    expect(focus).toHaveBeenCalledTimes(1);
  });
});

import { describe, it, expect, beforeEach } from "vitest";

import { lightRendition } from "./printLight";

// jsdom resolves an inline colour to the `rgb(...)` form a browser computes,
// which is what the rendition reads, so the ink and surface rules are testable
// here. What is not is SVG `fill`/`stroke` (jsdom computes neither) and what
// the page actually prints; both are held against a real browser.

/** The channels of an `rgb(...)` value. */
function channels(value: string): number[] {
  return /\d+/g.exec(value) ? value.match(/\d+/g)!.map(Number) : [];
}

/** An element's inline colour after the rendition. */
function inline(el: HTMLElement, property: string): string {
  return el.style.getPropertyValue(property);
}

/**
 * Whether the rendition set this property. Every element under test already
 * carries the colour the document wrote, so what marks the rendition's own
 * work is the `!important` it writes with.
 */
function repainted(el: HTMLElement, property: string): boolean {
  return el.style.getPropertyPriority(property) === "important";
}

beforeEach(() => {
  document.head.innerHTML = "";
  document.body.innerHTML = "";
  document.body.removeAttribute("style");
});

describe("lightRendition", () => {
  it("turns light text into dark ink (#1772)", () => {
    document.body.innerHTML = `<p id="t" style="color: #f4f4f5">Prose</p>`;

    lightRendition(document);

    const ink = channels(inline(document.getElementById("t")!, "color"));
    expect(ink).toHaveLength(3);
    // Near-black: the lightest source becomes the darkest ink.
    expect(Math.max(...ink)).toBeLessThan(60);
    expect(document.getElementById("t")!.style.getPropertyPriority("color")).toBe("important");
  });

  it("keeps the hue of an accent it darkens", () => {
    document.body.innerHTML = `<p id="t" style="color: #2dd3cb">Accent</p>`;

    lightRendition(document);

    const [red, green, blue] = channels(inline(document.getElementById("t")!, "color"));
    // Still teal: green and blue lead, red trails, and it is dark enough to
    // read on white.
    expect(green!).toBeGreaterThan(red!);
    expect(blue!).toBeGreaterThan(red!);
    expect(green!).toBeLessThan(160);
  });

  it("leaves text that is already readable on white alone", () => {
    document.body.innerHTML = `<p id="t" style="color: #15171a">Prose</p>`;

    lightRendition(document);

    expect(repainted(document.getElementById("t")!, "color")).toBe(false);
  });

  it("orders the ink it moves the way the document ordered it", () => {
    document.body.innerHTML =
      `<p id="body" style="color: #f4f4f5">Body</p>` +
      `<p id="muted" style="color: #a1a1aa">Muted</p>`;

    lightRendition(document);

    const body = channels(inline(document.getElementById("body")!, "color"));
    const muted = channels(inline(document.getElementById("muted")!, "color"));
    // The brighter of the two on a dark page is the darker of the two on white.
    expect(Math.max(...body)).toBeLessThan(Math.max(...muted));
  });

  it("repaints a dark panel as a pale tint of itself", () => {
    document.body.innerHTML = `<div id="p" style="background-color: #0f172a">Panel</div>`;

    lightRendition(document);

    const tint = channels(inline(document.getElementById("p")!, "background-color"));
    expect(Math.min(...tint)).toBeGreaterThan(220);
    // Still the panel's hue: blue leads, as it does in #0f172a.
    expect(tint[2]!).toBeGreaterThanOrEqual(tint[0]!);
  });

  it("leaves a mid-tone accent fill as the author wrote it", () => {
    document.body.innerHTML = `<div id="p" style="background-color: #2dd3cb">Badge</div>`;

    lightRendition(document);

    expect(repainted(document.getElementById("p")!, "background-color")).toBe(false);
  });

  it("leaves the page surfaces to the stylesheet", () => {
    document.body.setAttribute("style", "background-color: #020617");
    document.body.innerHTML = `<div class="pdf-page" id="page" style="background-color: #020617"></div>`;

    lightRendition(document);

    expect(repainted(document.body, "background-color")).toBe(false);
    expect(repainted(document.getElementById("page")!, "background-color")).toBe(false);
    const style = document.head.querySelector("style")!;
    expect(style.textContent).toContain(".pdf-page");
    expect(style.textContent).toContain("background: #fff !important");
    expect(style.textContent).toContain("print-color-adjust: exact");
  });

  it("leaves a colour it cannot read alone", () => {
    document.body.innerHTML = `<div id="p" style="background-image: linear-gradient(90deg,#2dd3cb,#0f77b2)"></div>`;

    lightRendition(document);

    expect(repainted(document.getElementById("p")!, "background-color")).toBe(false);
  });

  it("does nothing to a document with no view", () => {
    const detached = document.implementation.createHTMLDocument("detached");
    detached.body.innerHTML = `<p style="color: #ffffff">Prose</p>`;

    expect(() => lightRendition(detached)).not.toThrow();
    expect(detached.head.querySelector("style")).toBeNull();
  });
});

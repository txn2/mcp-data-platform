import { describe, expect, it, vi } from "vitest";
import { isSlideDeck, overviewMessage, PRINT_STEP, PRINTED_MESSAGE, printableDocument } from "./deck";

const DECK = `<!DOCTYPE html>\n<html lang="en">\n<head>\n<title>Deck</title>\n</head>\n<body><script src="/portal/vendor/reveal/reveal.js"></script></body></html>`;

/** The print step's script body, runnable against a window of the test's making. */
function printStepBody(): string {
  return PRINT_STEP.replace(/^<script>/, "").replace(/<\/script>$/, "");
}

/**
 * Runs the print step against a stand-in window: the same globals the step
 * touches, and nothing the runtime would bring. Returns the stand-in so a test
 * can play the runtime's part.
 */
function runPrintStep() {
  const listeners: Record<string, Array<() => void>> = {};
  const parent = { postMessage: vi.fn() };
  const win: Record<string, unknown> = {
    print: vi.fn(),
    parent,
    addEventListener: (type: string, fn: () => void) => {
      (listeners[type] ??= []).push(fn);
    },
  };
  new Function("window", printStepBody())(win);
  return {
    win,
    parent,
    load: () => listeners.load?.forEach((fn) => fn()),
  };
}

describe("isSlideDeck", () => {
  it("is true only for a document that loads the served runtime", () => {
    expect(isSlideDeck(DECK)).toBe(true);
    expect(isSlideDeck("<html><body><h1>Dashboard</h1></body></html>")).toBe(false);
    expect(isSlideDeck('<script src="https://cdn.example.com/reveal.js"></script>')).toBe(false);
  });
});

describe("overviewMessage", () => {
  it("is the runtime's postMessage form for toggleOverview", () => {
    expect(JSON.parse(overviewMessage())).toEqual({ method: "toggleOverview", args: [] });
  });
});

describe("printableDocument", () => {
  it("puts the print step inside <head>, after the doctype", () => {
    const doc = printableDocument(DECK);
    expect(doc.startsWith("<!DOCTYPE html>")).toBe(true);
    expect(doc.indexOf(PRINT_STEP)).toBe(DECK.indexOf("<head>") + "<head>".length);
    expect(doc).toContain('<script src="/portal/vendor/reveal/reveal.js">');
  });

  it("falls back to <html> when the document has no head tag", () => {
    const bare = '<!DOCTYPE html><html lang="en"><body>Hi</body></html>';
    const doc = printableDocument(bare);
    expect(doc.indexOf(PRINT_STEP)).toBe(bare.indexOf("<body>"));
  });

  it("puts the step first when the document opens with neither tag", () => {
    expect(printableDocument("<p>fragment</p>")).toBe(PRINT_STEP + "<p>fragment</p>");
  });
});

describe("the print step", () => {
  it("selects the print view before the runtime initializes and prints on pdf-ready", () => {
    const step = runPrintStep();
    const runtime = { configure: vi.fn(), on: vi.fn() };

    // The runtime's script tag assigns the global; the asset's own script
    // initializes it afterwards.
    step.win.Reveal = runtime;

    expect(step.win.Reveal).toBe(runtime);
    expect(runtime.configure).toHaveBeenCalledWith({ view: "print" });
    expect(runtime.on).toHaveBeenCalledWith("pdf-ready", expect.any(Function));

    // Load is not the print trigger for a deck: the pages are not laid out yet.
    step.load();
    expect(step.win.print).not.toHaveBeenCalled();

    const onReady = runtime.on.mock.calls[0]![1] as () => void;
    onReady();
    expect(step.win.print).toHaveBeenCalledTimes(1);
    expect(step.parent.postMessage).toHaveBeenCalledWith(PRINTED_MESSAGE, "*");

    // A second pdf-ready does not print twice.
    onReady();
    expect(step.win.print).toHaveBeenCalledTimes(1);
  });

  it("prints a document without the runtime once it has loaded", () => {
    const step = runPrintStep();
    step.load();
    expect(step.win.print).toHaveBeenCalledTimes(1);
    expect(step.parent.postMessage).toHaveBeenCalledWith(PRINTED_MESSAGE, "*");
  });

  it("still tells the page it printed when print() itself throws", () => {
    const step = runPrintStep();
    step.win.print = vi.fn(() => {
      throw new Error("refused");
    });
    step.load();
    expect(step.parent.postMessage).toHaveBeenCalledWith(PRINTED_MESSAGE, "*");
  });
});

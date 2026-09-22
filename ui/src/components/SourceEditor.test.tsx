import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { SourceEditor } from "./SourceEditor";

// The source editor's Wrap and Format controls (#1839), on the real CodeMirror
// and the real Prettier plugins. jsdom lays nothing out, so the editor's width
// is stubbed where a test needs one, and the HTML render check (which needs a
// browser) answers as told; e2e/interactive/source-format.spec.ts runs both
// for real.

const sameRendering = vi.hoisted(() => ({ value: true }));
vi.mock("@/lib/htmlRenderCheck", () => ({
  rendersSameText: vi.fn(async () => sameRendering.value),
}));

/** The editor under a parent that holds the buffer, as every caller does. */
function Harness({
  initial,
  contentType,
  onChange = () => {},
}: {
  initial: string;
  contentType: string;
  onChange?: (v: string) => void;
}) {
  const [content, setContent] = useState(initial);
  return (
    <>
      <SourceEditor
        content={content}
        contentType={contentType}
        onChange={(v) => {
          setContent(v);
          onChange(v);
        }}
      />
      <output data-testid="buffer">{content}</output>
    </>
  );
}

const wrapButton = () => screen.getByRole("button", { name: "Wrap" });
const cmContent = (container: HTMLElement) => container.querySelector(".cm-content")!;

/** Gives every CodeMirror scroller the width, leaving other elements at 0. */
function stubScrollerWidth(width: () => number) {
  Object.defineProperty(HTMLElement.prototype, "clientWidth", {
    configurable: true,
    get(this: HTMLElement) {
      return this.classList.contains("cm-scroller") ? width() : 0;
    },
  });
}

let observers: Array<() => void> = [];

beforeEach(() => {
  sameRendering.value = true;
  observers = [];
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private cb: () => void) {}
      observe() {
        observers.push(this.cb);
      }
      unobserve() {}
      disconnect() {
        observers = observers.filter((o) => o !== this.cb);
      }
    },
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  // Back to jsdom's own (always 0) clientWidth.
  delete (HTMLElement.prototype as { clientWidth?: number }).clientWidth;
});

describe("SourceEditor: Wrap", () => {
  it("toggles line wrapping without touching the document", async () => {
    const onChange = vi.fn();
    const { container } = render(<Harness initial="<p>a</p>" contentType="text/html" onChange={onChange} />);

    expect(wrapButton()).toHaveAttribute("aria-pressed", "false");
    expect(cmContent(container)).not.toHaveClass("cm-lineWrapping");

    fireEvent.click(wrapButton());
    expect(wrapButton()).toHaveAttribute("aria-pressed", "true");
    expect(cmContent(container)).toHaveClass("cm-lineWrapping");

    fireEvent.click(wrapButton());
    expect(wrapButton()).toHaveAttribute("aria-pressed", "false");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("opens wrapped when the longest line is wider than the editor", () => {
    stubScrollerWidth(() => 600);
    const { container } = render(<Harness initial={"<p>" + "x".repeat(4000) + "</p>"} contentType="text/html" />);
    expect(wrapButton()).toHaveAttribute("aria-pressed", "true");
    expect(cmContent(container)).toHaveClass("cm-lineWrapping");
  });

  it("opens unwrapped when every line fits", () => {
    stubScrollerWidth(() => 600);
    render(<Harness initial={"<p>short</p>\n<p>lines</p>"} contentType="text/html" />);
    expect(wrapButton()).toHaveAttribute("aria-pressed", "false");
  });

  it("decides when a hidden editor is first shown, and not again", async () => {
    let width = 0;
    stubScrollerWidth(() => width);
    render(<Harness initial={"x".repeat(4000)} contentType="text/html" />);
    expect(wrapButton()).toHaveAttribute("aria-pressed", "false");

    // The Source tab is shown: the editor gets a width and the observer fires.
    width = 600;
    act(() => observers.forEach((o) => o()));
    expect(wrapButton()).toHaveAttribute("aria-pressed", "true");

    // The reader turns it off; a later resize does not turn it back on.
    fireEvent.click(wrapButton());
    act(() => observers.forEach((o) => o()));
    expect(wrapButton()).toHaveAttribute("aria-pressed", "false");
  });
});

describe("SourceEditor: Format", () => {
  it("is offered for families with a formatter only", () => {
    const { unmount } = render(<Harness initial="{}" contentType="application/json" />);
    expect(screen.getByRole("button", { name: "Format" })).toBeInTheDocument();
    unmount();

    render(<Harness initial="x = 1" contentType="text/x-python" />);
    expect(screen.queryByRole("button", { name: "Format" })).not.toBeInTheDocument();
    expect(wrapButton()).toBeInTheDocument();
  });

  it("puts the formatted HTML in the buffer as an edit, for Save to store", async () => {
    const onChange = vi.fn();
    render(<Harness initial="<div><p>a</p><p>b</p></div>" contentType="text/html" onChange={onChange} />);

    fireEvent.click(screen.getByRole("button", { name: "Format" }));

    const expected = "<div>\n  <p>a</p>\n  <p>b</p>\n</div>\n";
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(expected));
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("buffer").textContent).toBe(expected);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("formats JSON", async () => {
    const onChange = vi.fn();
    render(<Harness initial={'{"a":1}'} contentType="application/json" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Format" }));
    await waitFor(() => expect(onChange).toHaveBeenCalledWith('{ "a": 1 }\n'));
  });

  it("leaves unparseable input as it was and says why", async () => {
    const onChange = vi.fn();
    render(<Harness initial="<p>hi</div>" contentType="text/html" onChange={onChange} />);

    fireEvent.click(screen.getByRole("button", { name: "Format" }));

    expect(await screen.findByRole("alert")).toHaveTextContent('Unexpected closing tag "div"');
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByTestId("buffer").textContent).toBe("<p>hi</div>");
  });

  it("leaves HTML as it was when the formatted document would display differently", async () => {
    sameRendering.value = false;
    const onChange = vi.fn();
    render(<Harness initial="<div><p>a</p></div>" contentType="text/html" onChange={onChange} />);

    fireEvent.click(screen.getByRole("button", { name: "Format" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("would change how this document's text displays");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("says so when the source is already formatted", async () => {
    const onChange = vi.fn();
    render(<Harness initial={'{ "a": 1 }\n'} contentType="application/json" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Format" }));
    expect(await screen.findByText("Already formatted")).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });
});

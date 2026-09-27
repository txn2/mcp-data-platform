import { afterEach, describe, expect, it, vi } from "vitest";
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { lineMarking, markLines, selectedLinesListener } from "./codemirrorLines";

let view: EditorView | null = null;
afterEach(() => {
  view?.destroy();
  view = null;
});

function editor(onSelect = vi.fn()) {
  view = new EditorView({
    state: EditorState.create({
      doc: "one\ntwo\nthree\nfour",
      extensions: [lineMarking(), selectedLinesListener(onSelect)],
    }),
    parent: document.body,
  });
  return { view, onSelect };
}

describe("codemirrorLines", () => {
  it("marks whole lines, ignoring ones the document does not have", () => {
    const { view } = editor();
    markLines(view, [3, 2, 9, 0]);
    const marked = [...view.dom.querySelectorAll(".cm-marked-line")].map((l) => l.textContent);
    expect(marked).toEqual(["two", "three"]);
    markLines(view, []);
    expect(view.dom.querySelectorAll(".cm-marked-line")).toHaveLength(0);
  });

  it("keeps a mark on its line through an edit above it", () => {
    const { view } = editor();
    markLines(view, [2]);
    view.dispatch({ changes: { from: 0, insert: "zero\n" } });
    expect([...view.dom.querySelectorAll(".cm-marked-line")].map((l) => l.textContent)).toEqual(["two"]);
  });

  it("reports the lines a selection covers, and null for a bare cursor", () => {
    const { view, onSelect } = editor();
    view.dispatch({ selection: { anchor: 5, head: 12 } });
    expect(onSelect).toHaveBeenLastCalledWith({ from: 2, to: 3 });
    view.dispatch({ selection: { anchor: 1 } });
    expect(onSelect).toHaveBeenLastCalledWith(null);
  });
});

import { RangeSetBuilder, StateEffect, StateField, type Extension } from "@codemirror/state";
import { Decoration, EditorView, type DecorationSet } from "@codemirror/view";

// Marking whole source lines in a CodeMirror editor, and reporting which lines
// the reader has selected. The script page uses both between its Flow and
// Source tabs (#1906): opening a card marks the lines it came from, and
// selecting lines marks the cards they produced.

/** setMarkedLines replaces the set of marked lines (1-based). */
export const setMarkedLines = StateEffect.define<number[]>();

const markedLine = Decoration.line({ class: "cm-marked-line" });

const markedLines = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(marks, tr) {
    let next = marks.map(tr.changes);
    for (const effect of tr.effects) {
      if (!effect.is(setMarkedLines)) continue;
      const builder = new RangeSetBuilder<Decoration>();
      const lines = [...new Set(effect.value)]
        .filter((n) => n >= 1 && n <= tr.state.doc.lines)
        .sort((a, b) => a - b);
      for (const n of lines) {
        const at = tr.state.doc.line(n).from;
        builder.add(at, at, markedLine);
      }
      next = builder.finish();
    }
    return next;
  },
  provide: (field) => EditorView.decorations.from(field),
});

const markedTheme = EditorView.baseTheme({
  ".cm-marked-line": { backgroundColor: "hsl(var(--chart-4) / 0.2)" },
});

/** lineMarking is the extension that draws marked lines. */
export function lineMarking(): Extension {
  return [markedLines, markedTheme];
}

/** markLines marks lines and scrolls the first of them into view. */
export function markLines(view: EditorView, lines: number[]) {
  const first = lines.filter((n) => n >= 1 && n <= view.state.doc.lines).sort((a, b) => a - b)[0];
  view.dispatch({
    effects: [
      setMarkedLines.of(lines),
      ...(first ? [EditorView.scrollIntoView(view.state.doc.line(first).from, { y: "center" })] : []),
    ],
  });
}

/** SelectedLines is a span of lines, both ends included. */
export interface SelectedLines {
  from: number;
  to: number;
}

/**
 * selectedLinesListener reports the lines the main selection covers each time
 * the selection changes, or null when nothing is selected (a bare cursor).
 */
export function selectedLinesListener(onSelect: (lines: SelectedLines | null) => void): Extension {
  return EditorView.updateListener.of((u) => {
    if (!u.selectionSet) return;
    const range = u.state.selection.main;
    if (range.empty) {
      onSelect(null);
      return;
    }
    onSelect({
      from: u.state.doc.lineAt(range.from).number,
      to: u.state.doc.lineAt(range.to).number,
    });
  });
}

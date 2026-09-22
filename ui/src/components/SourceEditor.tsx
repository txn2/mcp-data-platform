import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import CodeMirror from "@uiw/react-codemirror";
import { EditorView } from "@codemirror/view";
import { IndentIncrease, Loader2, WrapText } from "lucide-react";
import { Button } from "@/components/ui/button";
import { codeMirrorEditExtensions } from "@/lib/codemirror";
import { languageForContentType } from "@/components/renderers/registry";
import {
  formatErrorMessage,
  formatForEditor,
  formatParserFor,
  overflowsEditor,
  type FormatParser,
} from "@/lib/sourceFormat";

interface SourceEditorProps {
  content: string;
  contentType: string;
  fileName?: string;
  onChange: (value: string) => void;
}

/**
 * The editable source view.
 *
 * The language comes from the shared renderer registry, so the editor and the
 * read-only viewer always agree on what a content type is. JSON additionally
 * gets a parse linter: an edit that breaks the document is flagged in the
 * gutter while typing, rather than saving cleanly and failing later in the
 * viewer.
 *
 * Above the editor are Wrap and, for families with a formatter, Format
 * (#1839). Wrap changes only the display. It is on when the document first
 * lays out wider than the editor, which is how an agent's single-line HTML
 * arrives, and the reader's choice after that. Format replaces the buffer
 * with a reindented one as an ordinary edit: the document is dirty, undo
 * restores it, and nothing is stored until Save.
 */
export function SourceEditor({ content, contentType, fileName, onChange }: SourceEditorProps) {
  const viewRef = useRef<EditorView | null>(null);
  const hostRef = useRef<HTMLDivElement>(null);
  const parser = formatParserFor(contentType, fileName);

  const { wrap, toggleWrap, decideWrap } = useOpeningWrap(viewRef, hostRef, content);
  const { status, runFormat, clearStatus } = useFormat(viewRef, parser);

  const extensions = useMemo(() => {
    const base = codeMirrorEditExtensions(languageForContentType(contentType, fileName));
    return wrap ? [...base, EditorView.lineWrapping] : base;
  }, [contentType, fileName, wrap]);

  const handleChange = useCallback(
    (value: string) => {
      clearStatus();
      onChange(value);
    },
    [clearStatus, onChange],
  );

  const isDark =
    typeof document !== "undefined" && document.documentElement.classList.contains("dark");

  return (
    <div className="space-y-2">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1 pt-1">
          <FormatStatusText status={status} />
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {parser && (
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={runFormat}
              disabled={status.state === "running"}
              title="Reindent the source. Nothing is saved until you click Save."
            >
              {status.state === "running" ? <Loader2 className="animate-spin" /> : <IndentIncrease />}
              Format
            </Button>
          )}
          <Button
            type="button"
            variant={wrap ? "secondary" : "outline"}
            size="xs"
            onClick={toggleWrap}
            aria-pressed={wrap}
          >
            <WrapText />
            Wrap
          </Button>
        </div>
      </div>
      <div ref={hostRef}>
        <CodeMirror
          value={content}
          extensions={extensions}
          theme={isDark ? "dark" : "light"}
          onChange={handleChange}
          onCreateEditor={(view) => {
            viewRef.current = view;
            decideWrap();
          }}
          className="rounded-md border text-sm"
          minHeight="300px"
          maxHeight="calc(100vh - 200px)"
        />
      </div>
    </div>
  );
}

/**
 * Wrap state for the editor. It is undecided (and unwrapped) until the editor
 * is first laid out with a width, which for an editor mounted in a hidden
 * Source tab is when the tab is first shown. At that point it opens wrapped
 * when the longest line is wider than the text area. A click on Wrap decides
 * it for good.
 */
function useOpeningWrap(
  viewRef: RefObject<EditorView | null>,
  hostRef: RefObject<HTMLDivElement | null>,
  content: string,
) {
  const [wrap, setWrap] = useState<boolean | null>(null);
  const contentRef = useRef(content);
  useEffect(() => {
    contentRef.current = content;
  }, [content]);

  const decideWrap = useCallback(() => {
    const view = viewRef.current;
    if (!view) return;
    const opens = opensWrapped(view, contentRef.current);
    if (opens !== null) setWrap((current) => current ?? opens);
  }, [viewRef]);

  useEffect(() => {
    const host = hostRef.current;
    if (!host || wrap !== null) return;
    const observer = new ResizeObserver(() => decideWrap());
    observer.observe(host);
    decideWrap();
    return () => observer.disconnect();
  }, [hostRef, wrap, decideWrap]);

  const toggleWrap = useCallback(() => setWrap((current) => !current), []);

  return { wrap: wrap ?? false, toggleWrap, decideWrap };
}

/**
 * Whether the editor should open wrapped, or null while it has no width to
 * judge by. The text area is the scroller's width less the line-number
 * gutter.
 */
function opensWrapped(view: EditorView, text: string): boolean | null {
  const width = view.scrollDOM.clientWidth;
  if (width <= 0) return null;
  const gutters = view.dom.querySelector<HTMLElement>(".cm-gutters")?.offsetWidth ?? 0;
  return overflowsEditor(text, view.defaultCharacterWidth, width - gutters);
}

type FormatStatus =
  | { state: "idle" }
  | { state: "running" }
  | { state: "unchanged" }
  | { state: "error"; message: string };

const IDLE: FormatStatus = { state: "idle" };

const CHANGED_WHILE_FORMATTING =
  "The source changed while it was being formatted, so the result was discarded. Format again.";

/**
 * The Format action. The result goes into the editor as one transaction over
 * the whole document, so the editor's own change listener reports it (the
 * document becomes dirty) and undo takes it back. A failure leaves the buffer
 * untouched.
 */
function useFormat(viewRef: RefObject<EditorView | null>, parser: FormatParser | undefined) {
  const [status, setStatus] = useState<FormatStatus>(IDLE);

  useEffect(
    () => () => {
      viewRef.current = null;
    },
    [viewRef],
  );

  const runFormat = useCallback(async () => {
    const view = viewRef.current;
    if (!view || !parser) return;
    const before = view.state.doc.toString();
    setStatus({ state: "running" });

    let formatted: string;
    try {
      formatted = await formatForEditor(before, parser);
    } catch (err) {
      setStatus({ state: "error", message: formatErrorMessage(err) });
      return;
    }

    if (viewRef.current !== view || view.state.doc.toString() !== before) {
      setStatus({ state: "error", message: CHANGED_WHILE_FORMATTING });
      return;
    }
    if (formatted === before) {
      setStatus({ state: "unchanged" });
      return;
    }
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: formatted } });
    setStatus(IDLE);
  }, [viewRef, parser]);

  const clearStatus = useCallback(() => {
    setStatus((current) => (current.state === "running" ? current : IDLE));
  }, []);

  return { status, runFormat, clearStatus };
}

function FormatStatusText({ status }: { status: FormatStatus }) {
  if (status.state === "error") {
    return (
      <span role="alert" className="text-xs text-destructive">
        {status.message}
      </span>
    );
  }
  if (status.state === "unchanged") {
    return <span className="text-xs text-muted-foreground">Already formatted</span>;
  }
  return null;
}

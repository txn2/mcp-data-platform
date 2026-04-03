import { useState, useCallback, useMemo, useRef } from "react";
import CodeMirror, { type ReactCodeMirrorRef } from "@uiw/react-codemirror";
import { markdown } from "@codemirror/lang-markdown";
import { EditorView, placeholder as cmPlaceholder } from "@codemirror/view";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { cn } from "@/lib/utils";
import {
  Bold,
  Italic,
  Heading1,
  Heading2,
  Link,
  Code,
  List,
  ListOrdered,
  Quote,
  Minus,
  Eye,
  Pencil,
  Columns2,
} from "lucide-react";

type ViewMode = "edit" | "preview" | "split";

interface MarkdownEditorProps {
  value: string;
  onChange: (value: string) => void;
  readOnly?: boolean;
  minHeight?: string;
  placeholder?: string;
}

/** Insert markdown syntax around or at the cursor position. */
function insertMarkdown(
  ref: ReactCodeMirrorRef | null,
  before: string,
  after: string,
  placeholder: string,
) {
  const view = ref?.view;
  if (!view) return;

  const { from, to } = view.state.selection.main;
  const selected = view.state.sliceDoc(from, to);
  const text = selected || placeholder;
  const insert = `${before}${text}${after}`;

  view.dispatch({
    changes: { from, to, insert },
    selection: {
      anchor: from + before.length,
      head: from + before.length + text.length,
    },
  });
  view.focus();
}

const TOOLBAR_GROUPS = [
  [
    { icon: Bold, label: "Bold", before: "**", after: "**", placeholder: "bold text" },
    { icon: Italic, label: "Italic", before: "_", after: "_", placeholder: "italic text" },
    { icon: Code, label: "Inline code", before: "`", after: "`", placeholder: "code" },
  ],
  [
    { icon: Heading1, label: "Heading 1", before: "# ", after: "", placeholder: "Heading" },
    { icon: Heading2, label: "Heading 2", before: "## ", after: "", placeholder: "Heading" },
  ],
  [
    { icon: List, label: "Bullet list", before: "- ", after: "", placeholder: "item" },
    { icon: ListOrdered, label: "Numbered list", before: "1. ", after: "", placeholder: "item" },
    { icon: Quote, label: "Blockquote", before: "> ", after: "", placeholder: "quote" },
  ],
  [
    { icon: Link, label: "Link", before: "[", after: "](url)", placeholder: "link text" },
    { icon: Minus, label: "Horizontal rule", before: "\n---\n", after: "", placeholder: "" },
  ],
];

const VIEW_MODES: { mode: ViewMode; icon: typeof Pencil; label: string }[] = [
  { mode: "edit", icon: Pencil, label: "Editor" },
  { mode: "split", icon: Columns2, label: "Split" },
  { mode: "preview", icon: Eye, label: "Preview" },
];

export function MarkdownEditor({
  value,
  onChange,
  readOnly = false,
  minHeight = "400px",
  placeholder,
}: MarkdownEditorProps) {
  const [viewMode, setViewMode] = useState<ViewMode>("split");
  const cmRef = useRef<ReactCodeMirrorRef>(null);

  const isDark =
    typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark");

  const extensions = useMemo(
    () => [
      markdown(),
      EditorView.lineWrapping,
      ...(placeholder ? [cmPlaceholder(placeholder)] : []),
      EditorView.theme({
        "&": { fontSize: "13px" },
        ".cm-content": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", padding: "16px 0" },
        ".cm-gutters": { border: "none", background: "transparent" },
        ".cm-line": { padding: "0 16px" },
        ".cm-activeLine": { backgroundColor: isDark ? "rgba(255,255,255,0.03)" : "rgba(0,0,0,0.02)" },
        ".cm-activeLineGutter": { backgroundColor: "transparent" },
      }),
    ],
    [isDark, placeholder],
  );

  const handleToolbar = useCallback(
    (before: string, after: string, placeholder: string) => {
      insertMarkdown(cmRef.current, before, after, placeholder);
    },
    [],
  );

  const showEditor = viewMode === "edit" || viewMode === "split";
  const showPreview = viewMode === "preview" || viewMode === "split";

  return (
    <div className="flex h-full flex-col rounded-lg border bg-card overflow-hidden">
      {/* Toolbar */}
      <div className="flex items-center justify-between border-b bg-muted/30 px-2 py-1">
        {/* Formatting buttons */}
        <div className="flex items-center gap-0.5">
          {!readOnly &&
            TOOLBAR_GROUPS.map((group, gi) => (
              <div key={gi} className="flex items-center">
                {gi > 0 && (
                  <div className="mx-1 h-4 w-px bg-border" />
                )}
                {group.map((btn) => (
                  <button
                    key={btn.label}
                    type="button"
                    title={btn.label}
                    onClick={() => handleToolbar(btn.before, btn.after, btn.placeholder)}
                    className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  >
                    <btn.icon className="h-3.5 w-3.5" />
                  </button>
                ))}
              </div>
            ))}
        </div>

        {/* View mode toggle */}
        <div className="flex items-center gap-0.5 rounded-md border bg-background p-0.5">
          {VIEW_MODES.map(({ mode, icon: Icon, label }) => (
            <button
              key={mode}
              type="button"
              title={label}
              onClick={() => setViewMode(mode)}
              className={cn(
                "rounded px-2 py-1 text-xs font-medium transition-colors",
                viewMode === mode
                  ? "bg-primary/10 text-primary"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
            </button>
          ))}
        </div>
      </div>

      {/* Editor + Preview panes */}
      <div className="flex flex-1 overflow-hidden" style={{ minHeight }}>
        {showEditor && (
          <div
            className={cn(
              "overflow-auto",
              showPreview ? "w-1/2 border-r" : "w-full",
            )}
          >
            <CodeMirror
              ref={cmRef}
              value={value}
              extensions={extensions}
              theme={isDark ? "dark" : "light"}
              onChange={onChange}
              readOnly={readOnly}
              basicSetup={{
                lineNumbers: true,
                foldGutter: false,
                highlightActiveLine: true,
                bracketMatching: true,
              }}
              className="h-full text-sm [&_.cm-editor]:!outline-none [&_.cm-focused]:!outline-none"
              height="100%"
            />
          </div>
        )}
        {showPreview && (
          <div
            className={cn(
              "overflow-auto bg-background",
              showEditor ? "w-1/2" : "w-full",
            )}
          >
            <div className="p-6">
              {value.trim() ? (
                <MarkdownRenderer content={value} bare />
              ) : (
                <p className="text-sm italic text-muted-foreground">
                  Nothing to preview
                </p>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

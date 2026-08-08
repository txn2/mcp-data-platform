import { useMemo, useState } from "react";
import CodeMirror from "@uiw/react-codemirror";
import { EditorView } from "@codemirror/view";
import { WrapText } from "lucide-react";
import { Button } from "@/components/ui/button";
import { codeMirrorExtensions } from "@/lib/codemirror";
import type { CodeLanguage } from "./registry";
import { languageForContentType } from "./registry";

interface CodeRendererProps {
  content: string;
  /** Explicit language; falls back to the one the registry derives. */
  language?: CodeLanguage;
  contentType?: string;
  fileName?: string;
}

/**
 * Read-only source view with syntax highlighting, line numbers, code folding
 * and a wrap toggle. This is the viewer for the structured-text and code
 * families (XML, YAML, SQL, Python, JavaScript) and the raw view of JSON.
 *
 * Wrapping is off by default because these families are usually read by
 * structure: an unwrapped line keeps its indentation legible and horizontal
 * scrolling keeps row alignment intact. Long log lines are the case for turning
 * it on, so the toggle is one click away.
 */
export function CodeRenderer({ content, language, contentType, fileName }: CodeRendererProps) {
  const [wrap, setWrap] = useState(false);

  const resolved = language ?? (contentType ? languageForContentType(contentType, fileName) : undefined);

  const extensions = useMemo(() => {
    const base = codeMirrorExtensions(resolved);
    return wrap ? [...base, EditorView.lineWrapping] : base;
  }, [resolved, wrap]);

  const isDark = typeof document !== "undefined" && document.documentElement.classList.contains("dark");

  return (
    <div className="space-y-2" data-feedback-anchorable>
      <div className="flex items-center justify-between">
        <span className="text-xs text-muted-foreground">{resolved ? resolved.toUpperCase() : "Plain text"}</span>
        <Button
          type="button"
          variant={wrap ? "secondary" : "outline"}
          size="xs"
          onClick={() => setWrap((w) => !w)}
          aria-pressed={wrap}
        >
          <WrapText />
          Wrap
        </Button>
      </div>
      <CodeMirror
        value={content}
        extensions={extensions}
        theme={isDark ? "dark" : "light"}
        editable={false}
        basicSetup={{ lineNumbers: true, foldGutter: true, highlightActiveLine: false }}
        className="rounded-md border text-sm"
        maxHeight="min(70vh, 640px)"
      />
    </div>
  );
}

import { useMemo } from "react";
import CodeMirror from "@uiw/react-codemirror";
import { codeMirrorEditExtensions } from "@/lib/codemirror";
import { languageForContentType } from "@/components/renderers/registry";

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
 */
export function SourceEditor({ content, contentType, fileName, onChange }: SourceEditorProps) {
  const extensions = useMemo(
    () => codeMirrorEditExtensions(languageForContentType(contentType, fileName)),
    [contentType, fileName],
  );

  const isDark =
    typeof document !== "undefined" && document.documentElement.classList.contains("dark");

  return (
    <CodeMirror
      value={content}
      extensions={extensions}
      theme={isDark ? "dark" : "light"}
      onChange={onChange}
      className="rounded-md border text-sm"
      minHeight="300px"
      maxHeight="calc(100vh - 200px)"
    />
  );
}

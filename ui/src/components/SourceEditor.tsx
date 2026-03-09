import { useMemo } from "react";
import CodeMirror from "@uiw/react-codemirror";
import { html } from "@codemirror/lang-html";
import { markdown } from "@codemirror/lang-markdown";
import { xml } from "@codemirror/lang-xml";
import { javascript } from "@codemirror/lang-javascript";

interface SourceEditorProps {
  content: string;
  contentType: string;
  onChange: (value: string) => void;
}

function getExtensions(contentType: string) {
  const ct = contentType.toLowerCase();
  if (ct.includes("jsx") || ct.includes("javascript"))
    return [javascript({ jsx: true })];
  if (ct.includes("svg") || ct.includes("xml")) return [xml()];
  if (ct.includes("markdown") || ct.includes("md")) return [markdown()];
  if (ct.includes("html")) return [html()];
  return [];
}

export function SourceEditor({
  content,
  contentType,
  onChange,
}: SourceEditorProps) {
  const extensions = useMemo(() => getExtensions(contentType), [contentType]);
  const isDark =
    typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark");

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

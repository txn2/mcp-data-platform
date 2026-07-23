/**
 * CodeMirror language modes, in one place.
 *
 * Both the read-only code viewer and the editable source editor need the same
 * mapping from a family to a language extension, and both are lazily loaded
 * chunks, so keeping the table here means the modes are bundled once rather
 * than duplicated across the two entry points.
 */

import type { Extension } from "@codemirror/state";
import { json, jsonParseLinter } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { sql } from "@codemirror/lang-sql";
import { python } from "@codemirror/lang-python";
import { xml } from "@codemirror/lang-xml";
import { html } from "@codemirror/lang-html";
import { markdown } from "@codemirror/lang-markdown";
import { javascript } from "@codemirror/lang-javascript";
import { linter, lintGutter } from "@codemirror/lint";
import type { CodeLanguage } from "@/components/renderers/registry";

/** The language extension for a mode, or an empty set when there is none. */
export function codeMirrorExtensions(language: CodeLanguage | undefined): Extension[] {
  switch (language) {
    case "json":
      return [json()];
    case "yaml":
      return [yaml()];
    case "sql":
      return [sql()];
    case "python":
      return [python()];
    case "xml":
      return [xml()];
    case "html":
      return [html()];
    case "markdown":
      return [markdown()];
    case "javascript":
      return [javascript({ jsx: true })];
    default:
      return [];
  }
}

/**
 * Editing extensions for a mode: the language plus, where one exists, a linter
 * that flags syntax errors as you type. JSON is the case that matters: a
 * hand-edited document that no longer parses would otherwise save cleanly and
 * only fail later, in the viewer.
 */
export function codeMirrorEditExtensions(language: CodeLanguage | undefined): Extension[] {
  const base = codeMirrorExtensions(language);
  if (language === "json") {
    return [...base, linter(jsonParseLinter()), lintGutter()];
  }
  return base;
}

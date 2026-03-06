import { useEffect, useMemo } from "react";
import { transform } from "sucrase";

const CSP = [
  "default-src 'none'",
  "script-src 'unsafe-eval' 'unsafe-inline' https://esm.sh https://fonts.googleapis.com https://fonts.gstatic.com",
  "style-src 'unsafe-inline' https://fonts.googleapis.com",
  "img-src data: blob:",
  "font-src data: https://fonts.gstatic.com",
  "connect-src https://esm.sh https://fonts.googleapis.com https://fonts.gstatic.com",
].join("; ");

/**
 * Bare-specifier → absolute URL map. Used in the import map inside the
 * sandboxed iframe so that `import { useState } from "react"` etc. resolve.
 */
const BARE_IMPORT_MAP: Record<string, string> = {
  // React core: unbundled so all imports share one instance.
  // Using ?bundle here causes dual-instance bugs (useState → null).
  react: "https://esm.sh/react@19",
  "react/": "https://esm.sh/react@19/",
  "react-dom": "https://esm.sh/react-dom@19",
  "react-dom/": "https://esm.sh/react-dom@19/",
  "react-dom/client": "https://esm.sh/react-dom@19/client",
  // Leaf packages: bundle own deps but externalize react/react-dom.
  recharts: "https://esm.sh/recharts@2?bundle&external=react,react-dom",
  "lucide-react":
    "https://esm.sh/lucide-react@0.469?bundle&external=react",
};

const IMPORT_MAP = JSON.stringify({ imports: BARE_IMPORT_MAP });

/**
 * Escape closing script tags so injected code cannot break out of a
 * `<script>` block in the generated HTML.
 */
export function escapeScriptClose(code: string): string {
  return code.replace(/<\/script/gi, "<\\/script");
}

/**
 * Transform JSX to valid JavaScript using Sucrase.
 * Uses automatic JSX runtime so `<div>` becomes `_jsx("div", ...)` with an
 * auto-inserted `import { jsx as _jsx } from "react/jsx-runtime"`.
 */
export function transformJsx(code: string): string {
  const result = transform(code, {
    transforms: ["jsx"],
    jsxRuntime: "automatic",
    production: true,
  });
  return result.code;
}

/**
 * Detect the default-exported component name from JSX source.
 * Checks common patterns in order of specificity.
 */
export function findComponentName(code: string): string | null {
  // export default function Name / export default class Name
  const namedDefault = code.match(
    /export\s+default\s+(?:function|class)\s+([A-Z][A-Za-z0-9]*)/,
  );
  if (namedDefault?.[1]) return namedDefault[1];

  // export default Name;  (where Name is PascalCase)
  const reExport = code.match(/export\s+default\s+([A-Z][A-Za-z0-9]*)\s*;/);
  if (reExport?.[1]) return reExport[1];

  // Fallback: last PascalCase declaration (function/const/let/class)
  const matches = [
    ...code.matchAll(/(?:function|const|let|class)\s+([A-Z][A-Za-z0-9]*)/g),
  ];
  const last = matches[matches.length - 1];
  if (last?.[1]) return last[1];

  return null;
}

/**
 * Check if content already includes its own mount call.
 * Uses regex to match actual function calls rather than plain string includes,
 * reducing false positives from comments or string literals.
 */
function hasMountCode(content: string): boolean {
  return (
    /\bcreateRoot\s*\(/.test(content) ||
    /\bReactDOM\s*\.\s*render\s*\(/.test(content)
  );
}

const ERROR_STYLE =
  "color:#ef4444;background:#1e1e1e;padding:16px;font-size:13px;white-space:pre-wrap;overflow:auto;height:100%;font-family:monospace";

/**
 * Inline helper injected into the iframe to safely display error messages
 * using textContent (prevents innerHTML injection).
 */
const SHOW_ERROR_FN = `
function showError(text, tag, style) {
  var el = document.createElement(tag || 'pre');
  el.setAttribute('style', style || '${ERROR_STYLE}');
  el.textContent = text;
  var root = document.getElementById('root');
  root.textContent = '';
  root.appendChild(el);
}`;

export function JsxRenderer({ content }: { content: string }) {
  const blobUrl = useMemo(() => {
    let transformed: string;
    try {
      transformed = escapeScriptClose(transformJsx(content));
    } catch (e) {
      // If Sucrase fails, show the error in the iframe via textContent (safe).
      const errMsg =
        e instanceof Error ? e.message : "JSX transform failed";
      const html = `<!DOCTYPE html><html><body><pre id="e" style="${ERROR_STYLE}"></pre>
<script>document.getElementById('e').textContent=${JSON.stringify(errMsg)};</script></body></html>`;
      return URL.createObjectURL(new Blob([html], { type: "text/html" }));
    }

    if (hasMountCode(content)) {
      // Self-mounting path: content has its own createRoot/render call.
      // Inject transformed code with import map so bare specifiers resolve.
      const html = `<!DOCTYPE html>
<html>
<head>
  <meta http-equiv="Content-Security-Policy" content="${CSP}">
  <script type="importmap">${IMPORT_MAP}</script>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; padding: 16px; }
  </style>
</head>
<body>
  <div id="root"></div>
  <script type="module">
${SHOW_ERROR_FN}
window.onerror = function(msg, src, line, col, err) {
  showError(err && err.stack ? err.stack : msg);
};
window.addEventListener('unhandledrejection', function(e) {
  showError('Module load error: ' + (e.reason && e.reason.stack ? e.reason.stack : e.reason));
});
${transformed}
  </script>
</body>
</html>`;
      return URL.createObjectURL(new Blob([html], { type: "text/html" }));
    }

    // Auto-mount path: detect component name and render it.
    const componentName = findComponentName(content);
    const mountCall = componentName
      ? `createRoot(document.getElementById('root')).render(React.createElement(${componentName}));`
      : `showError('No component found. Use export default function MyComponent() {...}', 'p', 'color:#f59e0b;padding:16px;font-family:system-ui');`;

    const html = `<!DOCTYPE html>
<html>
<head>
  <meta http-equiv="Content-Security-Policy" content="${CSP}">
  <script type="importmap">${IMPORT_MAP}</script>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; padding: 16px; }
  </style>
</head>
<body>
  <div id="root"></div>
  <script type="module">
import React from 'react';
import { createRoot } from 'react-dom/client';

${SHOW_ERROR_FN}
window.onerror = function(msg, src, line, col, err) {
  showError(err && err.stack ? err.stack : msg);
};
window.addEventListener('unhandledrejection', function(e) {
  showError('Module load error: ' + (e.reason && e.reason.stack ? e.reason.stack : e.reason));
});

${transformed}

try {
  ${mountCall}
} catch(e) {
  showError(e.stack || e.message);
}
  </script>
</body>
</html>`;
    return URL.createObjectURL(new Blob([html], { type: "text/html" }));
  }, [content]);

  useEffect(() => {
    return () => {
      URL.revokeObjectURL(blobUrl);
    };
  }, [blobUrl]);

  return (
    <iframe
      sandbox="allow-scripts"
      src={blobUrl}
      className="w-full border border-border rounded-lg"
      style={{ height: "80vh" }}
      title="JSX Preview"
    />
  );
}

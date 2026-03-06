import { useEffect, useMemo, useRef } from "react";

const CSP = [
  "default-src 'none'",
  "script-src 'unsafe-eval' 'unsafe-inline' https://esm.sh blob:",
  "style-src 'unsafe-inline'",
  "img-src data: blob:",
  "font-src data:",
  "connect-src https://esm.sh blob:",
].join("; ");

/**
 * Bare-specifier → absolute URL map used for both the import map (self-mounting
 * path) and the blob module rewriter (auto-mount path). Single source of truth.
 */
const BARE_IMPORT_MAP: Record<string, string> = {
  react: "https://esm.sh/react@19?bundle",
  "react/": "https://esm.sh/react@19&bundle/",
  "react-dom/client": "https://esm.sh/react-dom@19/client?bundle",
  "react-dom/": "https://esm.sh/react-dom@19&bundle/",
  "react-dom": "https://esm.sh/react-dom@19?bundle",
  recharts: "https://esm.sh/recharts@2?bundle",
  "lucide-react": "https://esm.sh/lucide-react@0.469?bundle",
};

const IMPORT_MAP = JSON.stringify({ imports: BARE_IMPORT_MAP });

/** Replace bare import specifiers with absolute esm.sh URLs so blob modules resolve. */
function resolveImports(code: string): string {
  let resolved = code;
  const entries = Object.entries(BARE_IMPORT_MAP).sort(
    (a, b) => b[0].length - a[0].length,
  );
  for (const [bare, url] of entries) {
    resolved = resolved.split(`from '${bare}'`).join(`from '${url}'`);
    resolved = resolved.split(`from "${bare}"`).join(`from "${url}"`);
  }
  return resolved;
}

/**
 * If content lacks `export default`, detect the last PascalCase component and
 * append an export. Handles function, const, let, and class declarations.
 */
function ensureExport(code: string): string {
  if (/export\s+default/.test(code)) return code;
  const matches = [
    ...code.matchAll(/(?:function|const|let|class)\s+([A-Z][A-Za-z0-9]*)/g),
  ];
  const last = matches[matches.length - 1];
  if (last) {
    const name = last[1];
    return code + `\nexport default ${name};`;
  }
  return code;
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
  // Track module blob URLs for cleanup (auto-mount path creates one).
  const moduleBlobRef = useRef<string | null>(null);

  const blobUrl = useMemo(() => {
    // Revoke any previous module blob URL before creating a new one.
    if (moduleBlobRef.current) {
      URL.revokeObjectURL(moduleBlobRef.current);
      moduleBlobRef.current = null;
    }

    if (hasMountCode(content)) {
      // Self-mounting content: use import map + inline injection (existing behavior).
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
${content}
  </script>
</body>
</html>`;
      const blob = new Blob([html], { type: "text/html" });
      return URL.createObjectURL(blob);
    }

    // Auto-mount path: resolve imports, ensure default export, load as blob module.
    const resolvedCode = resolveImports(ensureExport(content));
    const moduleBlob = new Blob([resolvedCode], {
      type: "application/javascript",
    });
    const moduleBlobUrl = URL.createObjectURL(moduleBlob);
    moduleBlobRef.current = moduleBlobUrl;

    const html = `<!DOCTYPE html>
<html>
<head>
  <meta http-equiv="Content-Security-Policy" content="${CSP}">
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body { font-family: system-ui, sans-serif; padding: 16px; }
  </style>
</head>
<body>
  <div id="root"></div>
  <script type="module">
import React from 'https://esm.sh/react@19?bundle';
import { createRoot } from 'https://esm.sh/react-dom@19/client?bundle';

${SHOW_ERROR_FN}
window.onerror = function(msg, src, line, col, err) {
  showError(err && err.stack ? err.stack : msg);
};

try {
  const mod = await import("${moduleBlobUrl}");
  if (mod.default) {
    createRoot(document.getElementById('root'))
      .render(React.createElement(mod.default));
  } else {
    showError(
      'Module loaded but no default export found. Ensure your component uses export default.',
      'p',
      'color:#f59e0b;padding:16px;font-family:system-ui'
    );
  }
} catch(e) {
  showError(e.stack || e.message);
}
  </script>
</body>
</html>`;
    const htmlBlob = new Blob([html], { type: "text/html" });
    return URL.createObjectURL(htmlBlob);
  }, [content]);

  useEffect(() => {
    return () => {
      URL.revokeObjectURL(blobUrl);
      if (moduleBlobRef.current) {
        URL.revokeObjectURL(moduleBlobRef.current);
        moduleBlobRef.current = null;
      }
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

import { useEffect, useMemo } from "react";

const CSP = [
  "default-src 'none'",
  "script-src 'unsafe-eval' 'unsafe-inline' https://esm.sh blob:",
  "style-src 'unsafe-inline'",
  "img-src data: blob:",
  "font-src data:",
  "connect-src https://esm.sh blob:",
].join("; ");

const IMPORT_MAP = JSON.stringify({
  imports: {
    react: "https://esm.sh/react@19?bundle",
    "react/": "https://esm.sh/react@19&bundle/",
    "react-dom": "https://esm.sh/react-dom@19?bundle",
    "react-dom/": "https://esm.sh/react-dom@19&bundle/",
    "react-dom/client": "https://esm.sh/react-dom@19/client?bundle",
    recharts: "https://esm.sh/recharts@2?bundle",
    "lucide-react": "https://esm.sh/lucide-react@0.469?bundle",
  },
});

/**
 * Bare-specifier → absolute URL map for blob module imports.
 * Sorted longest-first at usage site so 'react-dom/client' matches before 'react-dom'.
 */
const BARE_IMPORT_MAP: Record<string, string> = {
  react: "https://esm.sh/react@19?bundle",
  "react/": "https://esm.sh/react@19&bundle/",
  "react-dom/client": "https://esm.sh/react-dom@19/client?bundle",
  "react-dom": "https://esm.sh/react-dom@19?bundle",
  recharts: "https://esm.sh/recharts@2?bundle",
  "lucide-react": "https://esm.sh/lucide-react@0.469?bundle",
};

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

/** If content lacks `export default`, detect the last PascalCase component and append an export. */
function ensureExport(code: string): string {
  if (/export\s+default/.test(code)) return code;
  const matches = [
    ...code.matchAll(/(?:function|const|let)\s+([A-Z][A-Za-z0-9]*)/g),
  ];
  const last = matches[matches.length - 1];
  if (last) {
    const name = last[1];
    return code + `\nexport default ${name};`;
  }
  return code;
}

/** Check if content already includes its own mount call. */
function hasMountCode(content: string): boolean {
  return content.includes("createRoot") || content.includes("ReactDOM.render");
}

const ERROR_STYLE =
  "color:#ef4444;background:#1e1e1e;padding:16px;font-size:13px;white-space:pre-wrap;overflow:auto;height:100%;font-family:monospace";

export function JsxRenderer({ content }: { content: string }) {
  const blobUrl = useMemo(() => {
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
window.onerror = function(msg, src, line, col, err) {
  document.getElementById('root').innerHTML =
    '<pre style="${ERROR_STYLE}">' + (err && err.stack ? err.stack : msg) + '</pre>';
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

window.onerror = function(msg, src, line, col, err) {
  document.getElementById('root').innerHTML =
    '<pre style="${ERROR_STYLE}">' + (err && err.stack ? err.stack : msg) + '</pre>';
};

try {
  const mod = await import("${moduleBlobUrl}");
  if (mod.default) {
    createRoot(document.getElementById('root'))
      .render(React.createElement(mod.default));
  } else {
    document.getElementById('root').innerHTML =
      '<p style="color:#f59e0b;padding:16px;font-family:system-ui">Module loaded but no default export found. Ensure your component uses export default.</p>';
  }
} catch(e) {
  document.getElementById('root').innerHTML =
    '<pre style="${ERROR_STYLE}">' + (e.stack || e.message) + '</pre>';
}
  </script>
</body>
</html>`;
    const htmlBlob = new Blob([html], { type: "text/html" });
    return URL.createObjectURL(htmlBlob);
  }, [content]);

  useEffect(() => {
    return () => URL.revokeObjectURL(blobUrl);
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

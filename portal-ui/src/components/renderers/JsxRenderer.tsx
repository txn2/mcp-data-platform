import { useMemo } from "react";

const CSP = [
  "default-src 'none'",
  "script-src 'unsafe-eval' 'unsafe-inline' https://esm.sh",
  "style-src 'unsafe-inline'",
  "img-src data: blob:",
  "font-src data:",
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

export function JsxRenderer({ content }: { content: string }) {
  const blobUrl = useMemo(() => {
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
${content}
  </script>
</body>
</html>`;
    const blob = new Blob([html], { type: "text/html" });
    return URL.createObjectURL(blob);
  }, [content]);

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

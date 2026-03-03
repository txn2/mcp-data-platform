import { useMemo } from "react";

export function HtmlRenderer({ content }: { content: string }) {
  const blobUrl = useMemo(() => {
    const blob = new Blob([content], { type: "text/html" });
    return URL.createObjectURL(blob);
  }, [content]);

  return (
    <iframe
      sandbox="allow-scripts"
      src={blobUrl}
      className="w-full border border-border rounded-lg"
      style={{ height: "80vh" }}
      title="HTML Preview"
    />
  );
}

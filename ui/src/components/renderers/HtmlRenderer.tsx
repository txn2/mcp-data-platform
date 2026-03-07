import { useEffect, useMemo } from "react";

export function HtmlRenderer({ content }: { content: string }) {
  const blobUrl = useMemo(() => {
    const blob = new Blob([content], { type: "text/html;charset=utf-8" });
    return URL.createObjectURL(blob);
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
      title="HTML Preview"
    />
  );
}

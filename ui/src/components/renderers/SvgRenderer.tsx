import { useMemo } from "react";
import DOMPurify from "dompurify";

export function SvgRenderer({ content }: { content: string }) {
  const sanitized = useMemo(
    () => DOMPurify.sanitize(content, { USE_PROFILES: { svg: true, svgFilters: true } }),
    [content],
  );

  return (
    <div
      className="flex items-center justify-center rounded-lg border bg-card p-6"
      dangerouslySetInnerHTML={{ __html: sanitized }}
    />
  );
}

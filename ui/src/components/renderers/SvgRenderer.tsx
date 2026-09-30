import { useMemo } from "react";
import DOMPurify from "dompurify";

// SvgRenderer draws an SVG on the image viewer's checkerboard (#1991), so
// white, black and colored artwork are all visible in both color schemes: on
// a solid card a white logo vanished in light mode and a black one in dark.
export function SvgRenderer({ content }: { content: string }) {
  const sanitized = useMemo(
    () => DOMPurify.sanitize(content, { USE_PROFILES: { svg: true, svgFilters: true } }),
    [content],
  );

  return (
    <div
      className="checkerboard flex items-center justify-center rounded-lg border p-6"
      dangerouslySetInnerHTML={{ __html: sanitized }}
    />
  );
}

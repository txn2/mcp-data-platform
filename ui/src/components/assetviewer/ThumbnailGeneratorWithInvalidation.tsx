import { Suspense, lazy, useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";

// The capturer carries html2canvas, the markdown renderer and the diagram
// engine. An asset that already has a thumbnail never mounts this component,
// and one that does not should not pay for the capturer before the page is
// readable, so it arrives on demand (#1351).
const ThumbnailGenerator = lazy(() =>
  import("@/components/ThumbnailGenerator").then((m) => ({ default: m.ThumbnailGenerator })),
);

export function ThumbnailGeneratorWithInvalidation({
  assetId,
  content,
  contentType,
  version,
  onDone,
}: {
  assetId: string;
  content: string;
  contentType: string;
  /** The asset version `content` was read at; recorded with the capture. */
  version?: number;
  onDone?: () => void;
}) {
  const qc = useQueryClient();
  const handleCaptured = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ["asset", assetId] });
    void qc.invalidateQueries({ queryKey: ["assets"] });
    onDone?.();
  }, [qc, assetId, onDone]);

  const handleFailed = useCallback(() => {
    onDone?.();
  }, [onDone]);

  return (
    <Suspense fallback={null}>
      <ThumbnailGenerator
        assetId={assetId}
        content={content}
        contentType={contentType}
        version={version}
        onCaptured={handleCaptured}
        onFailed={handleFailed}
      />
    </Suspense>
  );
}

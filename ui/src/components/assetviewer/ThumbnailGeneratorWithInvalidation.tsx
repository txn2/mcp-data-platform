import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ThumbnailGenerator } from "@/components/ThumbnailGenerator";

export function ThumbnailGeneratorWithInvalidation({
  assetId,
  content,
  contentType,
  onDone,
}: {
  assetId: string;
  content: string;
  contentType: string;
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
    <ThumbnailGenerator
      assetId={assetId}
      content={content}
      contentType={contentType}
      onCaptured={handleCaptured}
      onFailed={handleFailed}
    />
  );
}

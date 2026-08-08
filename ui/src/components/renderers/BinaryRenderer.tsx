import { Download, FileQuestion } from "lucide-react";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { familyLabel } from "./registry";

interface BinaryRendererProps {
  contentType: string;
  contentUrl?: string;
  fileName?: string;
  sizeBytes?: number;
}

/**
 * The fallback for content no viewer can present.
 *
 * It replaces what used to happen to these assets: raw bytes dumped into a
 * `<pre>` block, which renders as a screen of replacement characters and tells
 * the reader nothing. A card naming the type and size, with the download in
 * reach, is the honest answer.
 */
export function BinaryRenderer({ contentType, contentUrl, fileName, sizeBytes }: BinaryRendererProps) {
  return (
    <div
      className="flex flex-col items-center justify-center gap-4 rounded-lg border bg-card py-16 text-center"
      data-feedback-anchorable
    >
      <FileQuestion className="h-12 w-12 text-muted-foreground" />
      <div>
        <p className="text-lg font-medium">No preview for this file type</p>
        <p className="mt-1 text-sm text-muted-foreground">
          {familyLabel(contentType)}
          {sizeBytes ? ` · ${formatBytes(sizeBytes)}` : ""}
        </p>
        {fileName && <p className="mt-0.5 font-mono text-xs text-muted-foreground">{fileName}</p>}
      </div>
      {contentUrl && (
        <Button asChild>
          <a href={contentUrl} download={fileName}>
            <Download />
            Download
          </a>
        </Button>
      )}
    </div>
  );
}

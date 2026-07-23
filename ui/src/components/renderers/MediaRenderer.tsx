import { useState } from "react";
import { Download, FileAudio, FileText, FileVideo } from "lucide-react";
import { formatBytes } from "@/lib/format";

interface MediaRendererProps {
  contentUrl: string;
  contentType: string;
  fileName?: string;
  sizeBytes?: number;
}

/**
 * Audio and video players.
 *
 * Both point the native element straight at the content endpoint rather than
 * loading bytes into the page. That is what makes seeking work: the element
 * issues byte-range requests as the user scrubs, which the content endpoint
 * answers with 206 responses, so jumping to the middle of a long recording does
 * not first download the whole thing.
 *
 * There is deliberately no editing here: the platform stores media, it does
 * not transcode it.
 */
export function AudioRenderer(props: MediaRendererProps) {
  return (
    <MediaFrame {...props} icon={<FileAudio className="h-8 w-8 text-muted-foreground" />} kind="audio" />
  );
}

export function VideoRenderer(props: MediaRendererProps) {
  return (
    <MediaFrame {...props} icon={<FileVideo className="h-8 w-8 text-muted-foreground" />} kind="video" />
  );
}

function MediaFrame({
  contentUrl,
  contentType,
  fileName,
  sizeBytes,
  icon,
  kind,
}: MediaRendererProps & { icon: React.ReactNode; kind: "audio" | "video" }) {
  const [failed, setFailed] = useState(false);

  return (
    <div className="space-y-2" data-feedback-anchorable>
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{contentType}</span>
        {sizeBytes ? <span>· {formatBytes(sizeBytes)}</span> : null}
        <a
          href={contentUrl}
          download={fileName}
          className="ml-auto inline-flex items-center gap-1.5 rounded-md border px-2 py-1.5 text-foreground hover:bg-accent"
        >
          <Download className="h-3 w-3" />
          Download
        </a>
      </div>

      {failed ? (
        <div className="flex flex-col items-center gap-3 rounded-lg border bg-card p-8 text-center">
          {icon}
          <div>
            <p className="text-sm font-medium">This browser cannot play {contentType}</p>
            <p className="mt-1 text-xs text-muted-foreground">Download the file to play it in another application.</p>
          </div>
        </div>
      ) : (
        <div className="rounded-lg border bg-card p-4">
          {kind === "audio" ? (
            <audio
              src={contentUrl}
              controls
              preload="metadata"
              onError={() => setFailed(true)}
              className="w-full"
            >
              <track kind="captions" />
            </audio>
          ) : (
            <video
              src={contentUrl}
              controls
              preload="metadata"
              onError={() => setFailed(true)}
              className="mx-auto w-full"
              style={{ maxHeight: "min(70vh, 640px)" }}
            >
              <track kind="captions" />
            </video>
          )}
        </div>
      )}
    </div>
  );
}

interface PdfRendererProps {
  contentUrl: string;
  fileName?: string;
  sizeBytes?: number;
}

/**
 * PDF viewer.
 *
 * The document renders through `<object>` pointed at the content endpoint,
 * which hands it to the browser's own PDF viewer. `<object>` rather than
 * `<iframe>` because it degrades honestly: a browser with no PDF viewer renders
 * the fallback children below instead of a blank frame.
 *
 * There is deliberately no `sandbox` attribute. A sandboxed frame cannot
 * instantiate a plugin at all in Chrome, not with allow-scripts and not with
 * allow-same-origin, so a sandboxed PDF frame renders a broken-plugin icon and
 * nothing else. Containment comes from the serving side instead: the content
 * endpoint returns the object under a parsed `application/pdf` with
 * `X-Content-Type-Options: nosniff`, so the browser will not treat it as
 * anything else, and the public viewer's CSP pins `object-src` to 'self'.
 */
export function PdfRenderer({ contentUrl, fileName, sizeBytes }: PdfRendererProps) {
  return (
    <div className="space-y-2" data-feedback-anchorable>
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>PDF document</span>
        {sizeBytes ? <span>· {formatBytes(sizeBytes)}</span> : null}
        <a
          href={contentUrl}
          download={fileName}
          className="ml-auto inline-flex items-center gap-1.5 rounded-md border px-2 py-1.5 text-foreground hover:bg-accent"
        >
          <Download className="h-3 w-3" />
          Download
        </a>
      </div>
      <object
        data={contentUrl}
        type="application/pdf"
        aria-label={fileName || "PDF document"}
        className="w-full rounded-lg border bg-card"
        style={{ height: "min(80vh, 900px)" }}
      >
        <div className="flex flex-col items-center gap-3 p-8 text-center">
          <FileText className="h-8 w-8 text-muted-foreground" />
          <div>
            <p className="text-sm font-medium">This browser cannot display PDFs inline</p>
            <p className="mt-1 text-xs text-muted-foreground">Download the file to open it in a PDF reader.</p>
          </div>
          <a
            href={contentUrl}
            download={fileName}
            className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Download className="h-4 w-4" />
            Download
          </a>
        </div>
      </object>
    </div>
  );
}

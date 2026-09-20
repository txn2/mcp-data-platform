import { useState } from "react";
import { Download, FileAudio, FileVideo } from "lucide-react";
import { Button } from "@/components/ui/button";
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
        <Button asChild variant="outline" size="xs" className="ml-auto text-foreground">
          <a href={contentUrl} download={fileName}>
            <Download />
            Download
          </a>
        </Button>
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

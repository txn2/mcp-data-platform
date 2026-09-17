import { useCallback, useEffect, useRef, useState } from "react";
import { Presentation } from "lucide-react";

/**
 * Whether this browser lets a page fullscreen an element it holds. Read once
 * per render rather than at module load so a test can set it.
 */
function fullscreenAvailable(): boolean {
  return typeof document !== "undefined" && document.fullscreenEnabled === true;
}

/**
 * An HTML asset, framed.
 *
 * The document is handed to the frame as `srcdoc` rather than through a blob:
 * URL. A blob document has no path to resolve against, so a root-relative URL
 * in the artifact (`/portal/vendor/reveal/reveal.js`, the presentation runtime
 * the platform serves, #1767) named nothing at all; an srcdoc document resolves
 * such a URL against the page that framed it, which is the origin the artifact
 * was saved to. The sandbox is unchanged: `allow-scripts` without
 * `allow-same-origin`, so the artifact's script runs in an opaque origin with
 * no reach into the viewer's origin or storage.
 *
 * Present fullscreens the frame from this page and moves focus into it, so a
 * slide deck's keyboard navigation works from the first keystroke. The frame
 * also carries the fullscreen permission, which is what lets a document's own
 * fullscreen request (reveal.js answers the F key with one) succeed inside a
 * sandbox. The control is offered on every HTML asset, because the viewer
 * cannot tell a deck from a dashboard and fullscreening either is harmless.
 */
export function HtmlRenderer({ content }: { content: string }) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const [canPresent, setCanPresent] = useState(false);

  useEffect(() => {
    setCanPresent(fullscreenAvailable());
  }, []);

  const present = useCallback(() => {
    const frame = iframeRef.current;
    if (!frame) return;
    // A refused request (the browser wants a fresher gesture, or the page is
    // itself framed without permission) is not an error the reader can act on:
    // the frame stays where it was, and focus still moves into it so the
    // keyboard reaches the document.
    void frame.requestFullscreen?.().catch(() => undefined);
    frame.focus();
  }, []);

  return (
    <div className="flex flex-col gap-2">
      {canPresent && (
        <div className="flex justify-end">
          <button
            type="button"
            onClick={present}
            className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent transition-colors"
            title="Show this document fullscreen, with the keyboard focused on it"
          >
            <Presentation className="size-4" aria-hidden="true" />
            Present
          </button>
        </div>
      )}
      <iframe
        ref={iframeRef}
        sandbox="allow-scripts"
        allow="fullscreen"
        srcDoc={content}
        className="w-full border border-border rounded-lg"
        style={{ height: "80vh" }}
        title="HTML Preview"
      />
    </div>
  );
}

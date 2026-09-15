import { useCallback, useEffect, useRef, useState } from "react";
import { ImageOff, RefreshCw, TriangleAlert } from "lucide-react";
import { authedFetch } from "@/api/authed";
import { useClearAssetThumbnail } from "@/api/portal/hooks/assets";
import { useClearResourceThumbnail } from "@/api/resources/hooks";
import { AuthImg } from "@/components/AuthImg";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { useResolvedDark } from "@/stores/theme";
import { failureSentence, type CaptureFailure } from "@/lib/captureFailure";
import { resetCaptureAttempts } from "@/lib/thumbnailAttempts";
import { PanelCapture } from "@/components/thumbnail/PanelCapture";
import {
  isThumbnailSupported,
  thumbnailSrc,
  THUMBNAIL_SOURCE_LIMIT,
  type ThumbnailSubject,
  type ThumbnailTarget,
} from "@/lib/thumbnailSupport";

/**
 * A stored tile, and the control that asks for it to be taken again.
 *
 * The tile is what everyone else sees of this file -- in a grid, in a
 * collection, on a share page -- and its owner could neither see the stored
 * image beside the file nor do anything about it when it was wrong. A capture
 * that rendered the artifact's own error branch is a valid PNG and was stored
 * like any other, and only a new version would replace it (#1497).
 *
 * It is over a subject rather than over an asset because a managed resource has
 * exactly the same tile, taken by the same capturer and stored under the same
 * rule, and had neither the picture nor the button (#1568). Which kind this is
 * reaches here only as the target's kind, which decides which route the clear
 * is sent to.
 *
 * Recapturing belongs to whoever may change the file, so this is absent for a
 * reader who could not store the result, and for a type nothing rasterizes --
 * there is no tile to be wrong about.
 */
export function ThumbnailPanel({
  subject,
  canModify,
  onCapturing,
}: {
  subject: ThumbnailSubject;
  canModify: boolean;
  /**
   * Told when a capture this panel is running starts and stops.
   *
   * The asset viewer mounts a capturer of its own for an asset whose row says
   * one is wanted, and a press makes the row say exactly that: without this the
   * two would draw the same document twice over the reader's main thread. A
   * surface with no capturer of its own -- the resource viewer -- passes
   * nothing.
   */
  onCapturing?: (running: boolean) => void;
}) {
  const clear = useClearThumbnail(subject.target);
  const isDark = useResolvedDark();
  const [failed, setFailed] = useState(false);
  const capture = useRequestedCapture(subject, onCapturing);

  const src = thumbnailSrc(subject.target, subject.captures, isDark, subject.base);
  // The same question the viewer asks to decide whether to mount a capturer, so
  // the panel says "being taken" exactly while one is wanted -- including the
  // moment after the clear lands and before the new image arrives.
  //
  // A capture that FAILED is not one that is being taken, which is the whole of
  // #1752: the row still says a capture is wanted, and saying "in a moment"
  // about a document that threw is a promise nothing will keep.
  const capturing = subject.behind && capture.failure === null;
  const { markRecaptured, shown } = useReplaceCachedTile(src, capturing);
  // An image that would not load is a verdict on one URL, not on the file. The
  // panel is pointed at a new one when a replacement lands, and without this the
  // placeholder that stood in for the broken tile outlives it and reports "No
  // thumbnail stored" for a file that has one (#1501).
  useEffect(() => setFailed(false), [shown]);

  if (!capturable(subject, canModify)) {
    return null;
  }

  return (
    <div className="border-t pt-4" data-testid="thumbnail-panel">
      <SectionCard
        title="Thumbnail"
        action={
          <Button
            variant="outline"
            size="xs"
            onClick={() => {
              setFailed(false);
              // The queue has its own memory of what it has tried, and a press
              // on a file it has given up on has to clear that too: nothing on
              // the row moves, so there is nothing for it to notice (#1753).
              resetCaptureAttempts(subject.target);
              clear.mutate(subject.target.id, {
                onSuccess: () => {
                  markRecaptured();
                  capture.start();
                },
              });
            }}
            // Pressable even while a capture is wanted: a capture whose
            // references cannot load is discarded every time, so a file in
            // that state would otherwise leave its owner a control they could
            // never use again.
            disabled={clear.isPending || capture.running}
            title="Discard this image and take it again"
          >
            <RefreshCw /> {capture.failure ? "Try again" : "Recapture"}
          </Button>
        }
      >
        <PanelBody
          name={subject.name}
          shown={shown && !failed ? shown : undefined}
          capturing={capturing}
          failure={capture.failure}
          clearFailed={clear.isError}
          onImageFailed={() => setFailed(true)}
        />
      </SectionCard>
      {capture.element}
    </div>
  );
}

/**
 * The tile, or the sentence standing in for it: a capture in flight, one that
 * failed, or a file that simply has none.
 *
 * Its own component because the three states and their two colors put the panel
 * over the complexity budget, and because what it shows is one question -- what
 * is there to look at -- separate from the control above it.
 */
function PanelBody({
  name,
  shown,
  capturing,
  failure,
  clearFailed,
  onImageFailed,
}: {
  name: string;
  /** The tile's URL, absent when there is nothing to show. */
  shown?: string;
  capturing: boolean;
  failure: CaptureFailure | null;
  clearFailed: boolean;
  onImageFailed: () => void;
}) {
  const tone = failure ? "text-destructive" : "text-muted-foreground";
  return (
    <div className="space-y-2">
      {shown ? (
        // AuthImg rather than a bare <img>: an <img src> carries no X-API-Key,
        // so on an API-key session -- which is how the dev portal and every
        // API-key deployment sign in -- the tile 401s and the panel reports "No
        // thumbnail stored" for an image that exists, beside a control that
        // destroys it. The cards have always resolved their tiles this way;
        // this one did not.
        <AuthImg
          src={shown}
          alt={`Thumbnail for ${name}`}
          className="w-full rounded border bg-muted object-cover"
          // The panel is one image rather than a grid of them, and it is the
          // thing the reader opened the section for.
          loading="eager"
          onError={onImageFailed}
          // The same verdict on an API-key session, where the bytes are fetched
          // ahead of the element and a refusal produces no element to error.
          // Without it the panel waited on a load event that was never coming
          // and showed an empty box where the placeholder sentence belongs.
          onLoadFailed={onImageFailed}
        />
      ) : (
        <div
          className={`flex h-24 items-center justify-center gap-2 rounded border border-dashed text-xs ${tone}`}
          data-testid="thumbnail-placeholder"
        >
          {failure ? (
            <TriangleAlert className="size-4" aria-hidden />
          ) : (
            <ImageOff className="size-4" aria-hidden />
          )}
          {placeholder(capturing, failure)}
        </div>
      )}
      <p className={`text-xs ${tone}`} data-testid="thumbnail-explanation">
        {explain(capturing, failure)}
      </p>
      {failure && <p className="text-xs break-words text-muted-foreground">{failure.detail}</p>}
      {clearFailed && (
        <p className="text-xs text-destructive">Could not discard the stored image.</p>
      )}
    </div>
  );
}

/**
 * One capture the reader asked for, and how it went.
 *
 * The panel runs it rather than asking a parent to remount something, which is
 * what made the control inert on a file that had never been captured: nothing
 * on the row moved, so nothing downstream could tell a press had happened, and
 * for a managed resource there was no capturer in the page at all (#1753).
 *
 * The capturer is keyed on the press so each one is its own capture rather than
 * a reused finished result, and it is mounted only after a press: a file the
 * queue will get to on its own does not need a second capturer in the viewer.
 */
function useRequestedCapture(
  subject: ThumbnailSubject,
  onCapturing: ((running: boolean) => void) | undefined,
) {
  const [press, setPress] = useState(0);
  const [running, setRunning] = useState(false);
  const [failure, setFailure] = useState<CaptureFailure | null>(null);

  // A tile that arrived is the answer to whatever failed before it: another
  // capturer may have succeeded where this panel's attempt did not, and the
  // failure sentence under a picture would be nonsense.
  const behind = subject.behind;
  useEffect(() => {
    if (!behind) setFailure(null);
  }, [behind]);

  // A panel that goes away mid-capture must not leave its parent believing one
  // is still running: the asset viewer keeps its own capturer out of the way
  // while this is true, and the sidebar the panel lives in can be closed.
  const report = useRef(onCapturing);
  report.current = onCapturing;
  useEffect(() => () => report.current?.(false), []);

  // Stable identities: the capturer keys its own effects on the callbacks it
  // is handed, and a fresh one each render re-arms its capture timeout.
  const finish = useCallback((f: CaptureFailure | null) => {
    setRunning(false);
    report.current?.(false);
    setFailure(f);
  }, []);
  const captured = useCallback(() => finish(null), [finish]);

  return {
    running,
    failure,
    start: () => {
      setFailure(null);
      setRunning(true);
      report.current?.(true);
      setPress((n) => n + 1);
    },
    element:
      press > 0 && running ? (
        <PanelCapture key={press} subject={subject} onCaptured={captured} onFailed={finish} />
      ) : null,
  };
}

/**
 * The clear this target's kind is asked for through.
 *
 * Both hooks are called on every render -- they are hooks, and picking one by
 * kind at call time would be a conditional hook -- and the kind selects which
 * of the two mutations the panel drives. Each kind keeps its own mutation
 * beside the rest of its API surface, where its invalidations belong.
 */
function useClearThumbnail(target: ThumbnailTarget) {
  const asset = useClearAssetThumbnail();
  const resource = useClearResourceThumbnail();
  return target.kind === "resource" ? resource : asset;
}

/** Whether this reader has a tile to act on at all. */
function capturable(subject: ThumbnailSubject, canModify: boolean): boolean {
  return (
    canModify &&
    isThumbnailSupported(subject.contentType) &&
    subject.sizeBytes <= THUMBNAIL_SOURCE_LIMIT
  );
}

/** The three words in the empty box: a capture in flight, one that failed, or neither. */
function placeholder(capturing: boolean, failure: CaptureFailure | null): string {
  if (failure) return "Could not be made";
  return capturing ? "Being taken" : "No thumbnail stored";
}

/**
 * What the panel says about the image it is showing, about the one coming, or
 * about the one that could not be made.
 *
 * Written for the person looking at the page, not for the person who built it:
 * what is happening, when it will be over, and what to do if it is not. How
 * the picture is produced -- an idle-time capture in this browser, discarded
 * when a file the document links to fails to load -- is machinery, and reaches
 * the reader only as the one consequence they can act on.
 *
 * A failed capture said the same sentence as one in flight, forever, which on a
 * capture that threw was not true (#1752). Now it says so, and whether waiting
 * will change it.
 */
function explain(capturing: boolean, failure: CaptureFailure | null): string {
  if (failure) return failureSentence(failure);
  return capturing
    ? "The preview picture is being made and will appear here in a moment. If it never appears, a file this document links to could not be loaded."
    : "This image is what the file shows on cards, in collections, and on a share page.";
}

/**
 * Keeps this browser's copy of the tile from outliving a recapture.
 *
 * A recapture stores the new image at the same stamp, so the URL is the one the
 * browser already holds a cached copy of and the thumbnail route is cacheable
 * for an hour: without this the person who pressed the button keeps seeing the
 * picture they asked to replace. A reload-mode fetch replaces that cache entry
 * once the replacement has landed, and the counter it returns takes the panel's
 * own image off the in-memory copy so the new tile is on screen at once.
 *
 * Returns the URL to render and the callback the clear reports success to.
 */
function useReplaceCachedTile(src: string | undefined, capturing: boolean) {
  // Set when a clear this panel asked for succeeds, and spent below once the
  // replacement capture has landed.
  const [recaptured, setRecaptured] = useState(false);
  const [cacheBust, setCacheBust] = useState(0);
  // Whether the cleared row has reached this panel yet.
  //
  // The clear's own invalidation is what delivers it, so for the render the
  // press returns on, the row still carries the image the reader asked to be rid
  // of. Spending the trigger there reload-fetched that image's URL and left
  // nothing to spend when the replacement actually landed -- and put the panel
  // on a cache-busted URL of a row the server had already cleared, which 404s
  // and reports "No thumbnail stored" for a file that goes on to have one
  // (#1501). A capture cannot land before the cleared row does, because the
  // cleared row is what starts it.
  const clearLanded = useRef(false);
  useEffect(() => {
    if (capturing) clearLanded.current = true;
  }, [capturing]);

  useEffect(() => {
    if (!recaptured || capturing || !clearLanded.current || !src) return;
    setRecaptured(false);
    clearLanded.current = false;
    // Through the session's own credentials, for the reason the image is:
    // a bare fetch at an authenticated route is answered 401 on an API-key
    // session, and a 401 replaces nothing in the cache it was issued to clear.
    void authedFetch(src, { cache: "reload" })
      .catch(() => {})
      .finally(() => setCacheBust((n) => n + 1));
  }, [recaptured, capturing, src]);

  return {
    markRecaptured: () => setRecaptured(true),
    shown: src && cacheBust > 0 ? `${src}&r=${cacheBust}` : src,
  };
}

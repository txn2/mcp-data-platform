import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ImageOff, RefreshCw, TriangleAlert } from "lucide-react";
import { authedFetch } from "@/api/authed";
import { useClearAssetThumbnail } from "@/api/portal/hooks/assets";
import { useClearResourceThumbnail } from "@/api/resources/hooks";
import { AuthImg } from "@/components/AuthImg";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { useResolvedDark } from "@/stores/theme";
import type { FailedPart, ThumbnailSubject } from "@/lib/thumbnailSubject";
import {
  isThumbnailSupported,
  thumbnailSrc,
  THUMBNAIL_SOURCE_LIMIT,
  type ThumbnailTarget,
} from "@/lib/thumbnailSupport";

/**
 * How often the panel re-reads the file while its tile is being drawn. The
 * platform's renderer draws a tile within seconds of the file changing, so a
 * few seconds is how soon the reader sees it without a reload.
 */
export const DRAWING_POLL_MS = 3_000;

/**
 * A stored tile, and the control that asks for it to be drawn again.
 *
 * The tile is what everyone else sees of this file -- in a grid, in a
 * collection, on a share page -- so its owner sees it here, beside the file,
 * and can ask for another when it is wrong (#1497). A tile is drawn by the
 * platform's own renderer (#1787): asking for another discards the stored one,
 * and the renderer draws the file again within seconds. When it cannot, the
 * reason it gave is shown here, because that is the only place the person who
 * can fix the file will see it.
 *
 * It is over a subject rather than over an asset because a managed resource has
 * exactly the same tile (#1568). Which kind this is reaches here only as the
 * target's kind, which decides which route the clear is sent to.
 *
 * Absent for a reader who may not change the file, and for a type nothing
 * draws.
 */
export function ThumbnailPanel({
  subject,
  canModify,
}: {
  subject: ThumbnailSubject;
  canModify: boolean;
}) {
  const clear = useClearThumbnail(subject.target);
  const isDark = useResolvedDark();
  const [failed, setFailed] = useState(false);

  const src = thumbnailSrc(subject.target, subject.captures, isDark, subject.base);
  const drawing = subject.behind;
  useRefreshWhileDrawing(subject.target, drawing);
  const { markRecaptured, shown } = useReplaceCachedTile(src, drawing);
  // An image that would not load is a verdict on one URL, not on the file. The
  // panel is pointed at a new one when a replacement lands, and without this the
  // placeholder that stood in for the broken tile outlives it (#1501).
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
              clear.mutate(subject.target.id, { onSuccess: markRecaptured });
            }}
            // Pressing while a tile is being drawn asks for what is already
            // happening; pressing once it lands discards it and pays for the
            // whole draw again (#1791).
            disabled={clear.isPending || drawing}
            title={drawing ? "The picture is being drawn" : "Discard this image and draw it again"}
          >
            <RefreshCw /> {subject.failure ? "Try again" : "Recapture"}
          </Button>
        }
      >
        <PanelBody
          name={subject.name}
          shown={shown && !failed ? shown : undefined}
          drawing={drawing}
          failure={subject.failure}
          failedPart={subject.failedPart}
          clearFailed={clear.isError}
          onImageFailed={() => setFailed(true)}
        />
      </SectionCard>
    </div>
  );
}

/**
 * The tile, or the sentence standing in for it: one being drawn, one that
 * could not be, or a file that simply has none.
 */
function PanelBody({
  name,
  shown,
  drawing,
  failure,
  failedPart = "file",
  clearFailed,
  onImageFailed,
}: {
  name: string;
  /** The tile's URL, absent when there is nothing to show. */
  shown?: string;
  drawing: boolean;
  failure?: string;
  failedPart?: FailedPart;
  clearFailed: boolean;
  onImageFailed: () => void;
}) {
  // Red is for a file with no picture at all. A failure beside a picture that
  // was drawn is worth saying, not alarming about.
  const tone = failure && (!shown || failedPart === "file") ? "text-destructive" : "text-muted-foreground";
  return (
    <div className="space-y-2">
      {shown ? (
        // AuthImg rather than a bare <img>: an <img src> carries no X-API-Key,
        // so on an API-key session the tile 401s and the panel would report no
        // thumbnail for an image that exists.
        <AuthImg
          src={shown}
          alt={`Thumbnail for ${name}`}
          className="w-full rounded border bg-muted object-cover"
          loading="eager"
          onError={onImageFailed}
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
          {placeholder(drawing, failure)}
        </div>
      )}
      <p className={`text-xs ${tone}`} data-testid="thumbnail-explanation">
        {explain(drawing, failure ? failedPart : undefined)}
      </p>
      {failure && (
        <p className="text-xs break-words text-muted-foreground" data-testid="thumbnail-failure">
          {failure}
        </p>
      )}
      {clearFailed && (
        <p className="text-xs text-destructive">Could not discard the stored image.</p>
      )}
    </div>
  );
}

/**
 * Re-reads the file while its tile is being drawn, so the new tile -- or the
 * reason there is none -- reaches the panel without a reload.
 */
function useRefreshWhileDrawing(target: ThumbnailTarget, drawing: boolean): void {
  const qc = useQueryClient();
  const { kind, id } = target;
  useEffect(() => {
    if (!drawing) return;
    const timer = setInterval(() => {
      if (kind === "resource") {
        void qc.invalidateQueries({ queryKey: ["resources", id] });
        return;
      }
      void qc.invalidateQueries({ queryKey: ["asset", id] });
    }, DRAWING_POLL_MS);
    return () => clearInterval(timer);
  }, [qc, kind, id, drawing]);
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

/** The words in the empty box: a tile being drawn, one that could not be, or neither. */
function placeholder(drawing: boolean, failure: string | undefined): string {
  if (failure) return "Could not be drawn";
  return drawing ? "Being drawn" : "No thumbnail stored";
}

/** What follows every failure: when it is tried again. */
const RETRY = "It is tried again when the file changes, or now with Try again.";

/**
 * What the panel says about the image it is showing, about the one coming, or
 * about the one that could not be drawn. Written for the person looking at the
 * page: what is happening, and what to do if it does not.
 *
 * A failure is said of the part that failed (#1791). The light tile is drawn
 * first and kept when the dark one fails, so "could not be drawn" beside a
 * picture of this very version was untrue of the picture the reader was
 * looking at.
 */
function explain(drawing: boolean, failed: FailedPart | undefined): string {
  switch (failed) {
    case "dark":
      return `The dark-mode picture could not be drawn for this version of the file. ${RETRY}`;
    case "version":
      return `This picture is of an earlier version. This version could not be drawn. ${RETRY}`;
    case "file":
      return `The preview picture could not be drawn for this version of the file. ${RETRY}`;
  }
  return drawing
    ? "The preview picture is being drawn and will appear here in a few seconds."
    : "This image is what the file shows on cards, in collections, and on a share page.";
}

/**
 * Keeps this browser's copy of the tile from outliving a redraw.
 *
 * A redraw stores the new image at the same stamp, so the URL is the one the
 * browser already holds a cached copy of and the thumbnail route is cacheable
 * for an hour: without this the person who pressed the button keeps seeing the
 * picture they asked to replace. A reload-mode fetch replaces that cache entry
 * once the replacement has landed, and the counter it returns takes the panel's
 * own image off the in-memory copy so the new tile is on screen at once.
 *
 * Returns the URL to render and the callback the clear reports success to.
 */
function useReplaceCachedTile(src: string | undefined, drawing: boolean) {
  // Set when a clear this panel asked for succeeds, and spent below once the
  // replacement has landed.
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
  // (#1501). A tile cannot land before the cleared row does, because the
  // cleared row is what the renderer draws from.
  const clearLanded = useRef(false);
  useEffect(() => {
    if (drawing) clearLanded.current = true;
  }, [drawing]);

  useEffect(() => {
    if (!recaptured || drawing || !clearLanded.current || !src) return;
    setRecaptured(false);
    clearLanded.current = false;
    // Through the session's own credentials, for the reason the image is:
    // a bare fetch at an authenticated route is answered 401 on an API-key
    // session, and a 401 replaces nothing in the cache it was issued to clear.
    void authedFetch(src, { cache: "reload" })
      .catch(() => {})
      .finally(() => setCacheBust((n) => n + 1));
  }, [recaptured, drawing, src]);

  return {
    markRecaptured: () => setRecaptured(true),
    shown: src && cacheBust > 0 ? `${src}&r=${cacheBust}` : src,
  };
}

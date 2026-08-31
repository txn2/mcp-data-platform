import { useEffect, useRef, useState } from "react";
import { ImageOff, RefreshCw } from "lucide-react";
import { authedFetch } from "@/api/authed";
import { useClearAssetThumbnail } from "@/api/portal/hooks/assets";
import { useClearResourceThumbnail } from "@/api/resources/hooks";
import { AuthImg } from "@/components/AuthImg";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { useResolvedDark } from "@/stores/theme";
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
  onRecapture,
}: {
  subject: ThumbnailSubject;
  canModify: boolean;
  /**
   * Called once a clear this panel asked for has landed.
   *
   * Clearing a tile that is already cleared moves nothing on the row, so the row
   * cannot tell a second press from the first and the capturer the viewer has
   * already mounted would go on showing its finished result. This is what says a
   * press happened, so each one is its own capture (#1501).
   */
  onRecapture?: () => void;
}) {
  const clear = useClearThumbnail(subject.target);
  const isDark = useResolvedDark();
  const [failed, setFailed] = useState(false);

  const src = thumbnailSrc(subject.target, subject.captures, isDark, subject.base);
  // The same question the viewer asks to decide whether to mount a capturer, so
  // the panel says "being taken" exactly while one is wanted -- including the
  // moment after the clear lands and before the new image arrives.
  const capturing = subject.behind;
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
              clear.mutate(subject.target.id, {
                onSuccess: () => {
                  markRecaptured();
                  onRecapture?.();
                },
              });
            }}
            // Pressable even while a capture is wanted: a capture whose
            // references cannot load is discarded every time, so a file in
            // that state would otherwise leave its owner a control they could
            // never use again.
            disabled={clear.isPending}
            title="Discard this image and take it again"
          >
            <RefreshCw /> Recapture
          </Button>
        }
      >
        <div className="space-y-2">
          {shown && !failed ? (
            // AuthImg rather than a bare <img>: an <img src> carries no
            // X-API-Key, so on an API-key session -- which is how the dev portal
            // and every API-key deployment sign in -- the tile 401s and the
            // panel reports "No thumbnail stored" for an image that exists,
            // beside a control that destroys it. The cards have always resolved
            // their tiles this way; this one did not.
            <AuthImg
              src={shown}
              alt={`Thumbnail for ${subject.name}`}
              className="w-full rounded border bg-muted object-cover"
              // The panel is one image rather than a grid of them, and it is
              // the thing the reader opened the section for.
              loading="eager"
              onError={() => setFailed(true)}
              // The same verdict on an API-key session, where the bytes are
              // fetched ahead of the element and a refusal produces no element
              // to error. Without it the panel waited on a load event that was
              // never coming and showed an empty box where the placeholder
              // sentence belongs.
              onLoadFailed={() => setFailed(true)}
            />
          ) : (
            <div className="flex h-24 items-center justify-center gap-2 rounded border border-dashed text-xs text-muted-foreground">
              <ImageOff className="size-4" aria-hidden />
              {capturing ? "Being taken" : "No thumbnail stored"}
            </div>
          )}
          <p className="text-xs text-muted-foreground">{explain(capturing)}</p>
          {clear.isError && (
            <p className="text-xs text-destructive">Could not discard the stored image.</p>
          )}
        </div>
      </SectionCard>
    </div>
  );
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

/** What the panel says about the image it is showing, or about the one coming. */
function explain(capturing: boolean): string {
  return capturing
    ? "A new capture is taken in the browser the next time this page is idle. A capture whose referenced files did not load is discarded, so a file naming something that is gone keeps waiting."
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

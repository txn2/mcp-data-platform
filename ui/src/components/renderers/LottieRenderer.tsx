import { useEffect, useRef, useState } from "react";
import lottie, { type AnimationItem } from "lottie-web/build/player/lottie_light";
import { Pause, Play } from "lucide-react";
import { parseAnimation, readDotLottie, type Animation } from "./lottieSource";

interface Props {
  /** A Lottie JSON source, already read. */
  content?: string;
  /** A dotLottie archive's address, read as bytes. */
  contentUrl?: string;
  /** Draw one still frame filling the container, as a tile does: the last one. */
  still?: boolean;
  /** Called with "" once a still frame is drawn, or with why it is not. */
  onDrawn?: (reason: string) => void;
}

/** The animation, from the text in hand or the archive at contentUrl. */
async function loadAnimation(content: string | undefined, contentUrl: string | undefined): Promise<Animation> {
  if (content !== undefined) return parseAnimation(content);
  if (!contentUrl) throw new Error("there is nothing to play");
  const res = await fetch(contentUrl, { credentials: "same-origin" });
  if (!res.ok) throw new Error(`the file could not be read (${res.status})`);
  return readDotLottie(new Uint8Array(await res.arrayBuffer()));
}

/**
 * A Lottie animation (#2308), played with play, pause and scrub, or drawn as
 * its last frame for a tile. lottieSource says why nothing it plays runs or
 * fetches.
 */
export function LottieRenderer({ content, contentUrl, still, onDrawn }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const anim = useRef<AnimationItem | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [playing, setPlaying] = useState(!still);
  const [frame, setFrame] = useState(0);
  const [frames, setFrames] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setError(null);
    loadAnimation(content, contentUrl)
      .then((data) => {
        if (cancelled || !host.current) return;
        const item = lottie.loadAnimation({
          container: host.current,
          renderer: "svg",
          loop: true,
          autoplay: !still,
          animationData: data,
          rendererSettings: { preserveAspectRatio: "xMidYMid meet" },
        });
        anim.current = item;
        const total = Math.max(0, Math.floor(data.op - data.ip));
        setFrames(total);
        item.addEventListener("enterFrame", () => setFrame(Math.floor(item.currentFrame)));
        item.addEventListener("DOMLoaded", () => {
          if (still) {
            item.goToAndStop(Math.max(0, total - 1), true);
            onDrawn?.("");
          }
        });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const reason = err instanceof Error ? err.message : String(err);
        setError(reason);
        onDrawn?.(`the animation could not be played: ${reason}`);
      });
    return () => {
      cancelled = true;
      anim.current?.destroy();
      anim.current = null;
    };
  }, [content, contentUrl, still, onDrawn]);

  if (error) {
    return (
      <div role="alert" className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm">
        <p className="font-medium">This animation could not be played.</p>
        <pre className="mt-2 whitespace-pre-wrap font-mono text-xs text-muted-foreground">{error}</pre>
      </div>
    );
  }
  if (still) return <div ref={host} data-testid="lottie-still" style={{ width: "100%", height: "100%" }} />;

  const toggle = () => {
    if (!anim.current) return;
    if (playing) anim.current.pause();
    else anim.current.play();
    setPlaying(!playing);
  };

  return (
    <div className="flex flex-col items-center gap-3">
      <div ref={host} data-testid="lottie-player" className="w-full max-w-3xl" style={{ aspectRatio: "16 / 9" }} />
      <div className="flex w-full max-w-3xl items-center gap-3">
        <button
          type="button"
          onClick={toggle}
          className="inline-flex size-8 items-center justify-center rounded-md border hover:bg-accent"
          aria-label={playing ? "Pause" : "Play"}
        >
          {playing ? <Pause className="size-4" /> : <Play className="size-4" />}
        </button>
        <input
          type="range"
          aria-label="Frame"
          className="flex-1"
          min={0}
          max={Math.max(0, frames - 1)}
          value={Math.min(frame, Math.max(0, frames - 1))}
          onChange={(e) => {
            const f = Number(e.target.value);
            anim.current?.goToAndStop(f, true);
            setPlaying(false);
            setFrame(f);
          }}
        />
        <span className="w-20 text-right font-mono text-xs text-muted-foreground">
          {frame}/{Math.max(0, frames - 1)}
        </span>
      </div>
    </div>
  );
}

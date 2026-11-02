import { useEffect, useMemo, useRef, useState } from "react";
import DOMPurify from "dompurify";
import { Excalidraw, exportToSvg, getSceneVersion, restore, serializeAsJSON } from "@excalidraw/excalidraw";
import "@excalidraw/excalidraw/index.css";

interface Props {
  content: string;
  /**
   * Called with the scene as stored JSON whenever an editor changes it. Absent
   * for a reader without edit access and on every share: the scene is drawn
   * read-only.
   */
  onEdit?: (scene: string) => void;
  /** Draw the scene as a still picture filling its container, as a tile does. */
  still?: boolean;
  /** The color scheme a still picture is drawn in. */
  dark?: boolean;
  /** Called with "" once a still picture is drawn, or with why it is not. */
  onDrawn?: (reason: string) => void;
}

interface Scene {
  elements: unknown[];
  appState?: Record<string, unknown>;
  files?: Record<string, unknown>;
}

/** The stored scene, or the reason it cannot be read. */
function parseScene(content: string): Scene | string {
  try {
    const scene = JSON.parse(content) as Scene;
    if (!scene || !Array.isArray(scene.elements)) return "the scene has no elements";
    return scene;
  } catch (err) {
    return err instanceof Error ? err.message : String(err);
  }
}

/** Whether the page is drawn in its dark scheme. */
function pageIsDark(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
}

/**
 * An Excalidraw scene (#2306): the editor itself, read-only unless the reader
 * may edit, and a still SVG for the tile.
 *
 * An editor's change is handed up as the scene's JSON (onEdit) and saved
 * through the asset page's own save, so it is a new version that history and
 * diff read like any other, checked by the same save check an agent's write
 * passes. The scene's own edits coming back as content do not reset the
 * canvas. Embedding a web page is refused in the editor, as it is on save.
 */
export function ExcalidrawRenderer({ content, onEdit, still, dark, onDrawn }: Props) {
  const scene = useMemo(() => parseScene(content), [content]);
  useEffect(() => {
    if (typeof scene === "string") onDrawn?.(`the scene could not be read: ${scene}`);
  }, [scene, onDrawn]);
  if (typeof scene === "string") {
    return (
      <div role="alert" className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm">
        <p className="font-medium">This scene could not be drawn.</p>
        <pre className="mt-2 whitespace-pre-wrap font-mono text-xs text-muted-foreground">{scene}</pre>
      </div>
    );
  }
  if (still) return <StillScene scene={scene} dark={!!dark} onDrawn={onDrawn} />;
  return <SceneEditor content={content} scene={scene} onEdit={onEdit} />;
}

function SceneEditor({ content, scene, onEdit }: { content: string; scene: Scene; onEdit?: (s: string) => void }) {
  // The last scene this editor handed up, so the same text coming back as
  // content does not remount the canvas under the person drawing.
  const emitted = useRef<string | null>(null);
  const version = useRef<number | null>(null);
  const [mount, setMount] = useState(0);
  useEffect(() => {
    if (content !== emitted.current) setMount((m) => m + 1);
  }, [content]);

  return (
    <div className="h-[70vh] min-h-[420px] w-full overflow-hidden rounded-lg border" data-testid="excalidraw-scene">
      <Excalidraw
        key={mount}
        initialData={{ elements: scene.elements as never, appState: { ...(scene.appState ?? {}), collaborators: new Map() } as never, files: (scene.files ?? {}) as never }}
        viewModeEnabled={!onEdit}
        theme={pageIsDark() ? "dark" : "light"}
        validateEmbeddable={() => false}
        onChange={(elements, appState, files) => {
          if (!onEdit) return;
          const v = getSceneVersion(elements);
          if (version.current === null) {
            version.current = v;
            return;
          }
          if (v === version.current) return;
          version.current = v;
          const json = serializeAsJSON(elements, appState, files, "local");
          emitted.current = json;
          onEdit(json);
        }}
      />
    </div>
  );
}

function StillScene({ scene, dark, onDrawn }: { scene: Scene; dark: boolean; onDrawn?: (r: string) => void }) {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let cancelled = false;
    // A scene an agent wrote names only the fields it cares about; restore
    // fills the rest with Excalidraw's defaults, as the editor does on load.
    const restored = restore({ elements: scene.elements as never, appState: scene.appState as never, files: scene.files as never }, null, null);
    exportToSvg({
      elements: restored.elements,
      appState: { ...restored.appState, exportBackground: false, exportWithDarkMode: dark },
      files: restored.files,
    })
      .then((svg: SVGSVGElement) => {
        if (cancelled || !host.current) return;
        const clean = DOMPurify.sanitize(svg.outerHTML, {
          USE_PROFILES: { svg: true, svgFilters: true },
          ADD_TAGS: ["style"],
          RETURN_DOM: true,
        }) as unknown as HTMLElement;
        const el = clean.querySelector("svg");
        if (!el) throw new Error("the scene did not draw");
        el.setAttribute("width", "100%");
        el.setAttribute("height", "100%");
        el.setAttribute("preserveAspectRatio", "xMidYMid meet");
        host.current.replaceChildren(document.importNode(el, true));
        onDrawn?.("");
      })
      .catch((err: unknown) => {
        if (!cancelled) onDrawn?.(`the scene could not be drawn: ${err instanceof Error ? err.message : String(err)}`);
      });
    return () => {
      cancelled = true;
    };
  }, [scene, dark, onDrawn]);
  return <div ref={host} style={{ width: "100%", height: "100%" }} />;
}

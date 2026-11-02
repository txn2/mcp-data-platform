import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor, cleanup, act, fireEvent } from "@testing-library/react";

// The drawn sources of #2302, #2306 and #2308. Excalidraw and lottie-web draw
// on a canvas or a live SVG timeline jsdom does not run, so each is stood in
// for here; their own behavior is the libraries'. What is tested is the
// viewer's part: what it fetches, what it hands the library, and what it
// hands back.

const excalidrawProps: { current: Record<string, unknown> | null } = { current: null };
vi.mock("@excalidraw/excalidraw", () => ({
  Excalidraw: (props: Record<string, unknown>) => {
    excalidrawProps.current = props;
    return <div data-testid="excalidraw-canvas" />;
  },
  exportToSvg: vi.fn(async () => {
    const el = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    el.innerHTML = `<rect width="10" height="10"/><script>alert(1)</script>`;
    return el;
  }),
  getSceneVersion: (els: { version?: number }[]) => els.reduce((n, e) => n + (e.version ?? 0), 0),
  restore: (data: { elements: unknown[]; appState?: object; files?: object }) => ({
    elements: data.elements,
    appState: data.appState ?? {},
    files: data.files ?? {},
  }),
  serializeAsJSON: (els: unknown[]) => JSON.stringify({ type: "excalidraw", elements: els }),
}));
vi.mock("@excalidraw/excalidraw/index.css", () => ({}));

const lottieCalls: Record<string, unknown>[] = [];
vi.mock("lottie-web/build/player/lottie_light", () => ({
  default: {
    loadAnimation: (opts: Record<string, unknown>) => {
      lottieCalls.push(opts);
      const listeners: Record<string, () => void> = {};
      const item = {
        currentFrame: 0,
        addEventListener: (ev: string, fn: () => void) => {
          listeners[ev] = fn;
          if (ev === "DOMLoaded") setTimeout(fn, 0);
        },
        goToAndStop: vi.fn(),
        play: vi.fn(),
        pause: vi.fn(),
        destroy: vi.fn(),
      };
      lottieCalls.push({ item });
      return item;
    },
  },
}));

import { D2Renderer } from "./D2Renderer";
import { ExcalidrawRenderer } from "./ExcalidrawRenderer";
import { LottieRenderer } from "./LottieRenderer";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  lottieCalls.length = 0;
  excalidrawProps.current = null;
});

describe("D2Renderer (#2302)", () => {
  it("reads the platform's drawing from the content route and places it cleaned", async () => {
    const fetchMock = vi.fn(async () => new Response(`<svg xmlns="http://www.w3.org/2000/svg"><text>vpc</text><script>x()</script></svg>`, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const onDrawn = vi.fn();
    render(<D2Renderer content="a -> b" contentUrl="/api/v1/portal/assets/a1/content" onDrawn={onDrawn} />);
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""));
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/portal/assets/a1/content?render=svg", { credentials: "same-origin" });
    const host = screen.getByTestId("d2-drawing");
    expect(host.textContent).toContain("vpc");
    expect(host.querySelector("script")).toBeNull();
  });

  it("shows the platform's refusal", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ detail: "the diagram could not be drawn: bad shape" }), { status: 422 })));
    const onDrawn = vi.fn();
    render(<D2Renderer content="x" contentUrl="/c" onDrawn={onDrawn} />);
    expect(await screen.findByText("This diagram could not be drawn.")).toBeInTheDocument();
    expect(screen.getByText(/bad shape/)).toBeInTheDocument();
    expect(onDrawn.mock.calls[0]![0]).toMatch(/could not be drawn/);
  });

  it("says so when there is no address to draw from", () => {
    render(<D2Renderer content="x" />);
    expect(screen.getByText("This diagram could not be drawn.")).toBeInTheDocument();
  });
});

describe("ExcalidrawRenderer (#2306)", () => {
  const scene = JSON.stringify({ type: "excalidraw", elements: [{ id: "a", type: "rectangle", version: 1 }] });

  it("is read-only without an editor", () => {
    render(<ExcalidrawRenderer content={scene} />);
    expect(screen.getByTestId("excalidraw-canvas")).toBeInTheDocument();
    expect(excalidrawProps.current!.viewModeEnabled).toBe(true);
  });

  it("hands an editor's change up as the scene's JSON, ignoring the mount", () => {
    const onEdit = vi.fn();
    render(<ExcalidrawRenderer content={scene} onEdit={onEdit} />);
    expect(excalidrawProps.current!.viewModeEnabled).toBe(false);
    const onChange = excalidrawProps.current!.onChange as (els: unknown[], app: unknown, files: unknown) => void;
    act(() => onChange([{ id: "a", version: 1 }], {}, {}));
    expect(onEdit).not.toHaveBeenCalled();
    act(() => onChange([{ id: "a", version: 2 }], {}, {}));
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(JSON.parse(onEdit.mock.calls[0]![0] as string)).toMatchObject({ type: "excalidraw" });
    act(() => onChange([{ id: "a", version: 2 }], {}, {}));
    expect(onEdit).toHaveBeenCalledTimes(1);
    const validate = excalidrawProps.current!.validateEmbeddable as (l: string) => boolean;
    expect(validate("https://www.youtube.com/watch?v=x")).toBe(false);
  });

  it("draws a still, cleaned picture for a tile", async () => {
    const onDrawn = vi.fn();
    const { container } = render(<ExcalidrawRenderer content={scene} still dark onDrawn={onDrawn} />);
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""));
    expect(container.querySelector("rect")).not.toBeNull();
    expect(container.querySelector("script")).toBeNull();
  });

  it("says why a scene cannot be read", async () => {
    const onDrawn = vi.fn();
    render(<ExcalidrawRenderer content="{" onDrawn={onDrawn} />);
    expect(screen.getByText("This scene could not be drawn.")).toBeInTheDocument();
    await waitFor(() => expect(onDrawn.mock.calls[0]![0]).toMatch(/could not be read/));
  });
});

describe("LottieRenderer (#2308)", () => {
  const anim = JSON.stringify({ fr: 30, ip: 0, op: 60, layers: [], assets: [{ id: "i", u: "https://cdn.example.com/", p: "a.png" }] });

  it("plays with the light player, nothing external, and pauses and scrubs", async () => {
    render(<LottieRenderer content={anim} />);
    await waitFor(() => expect(lottieCalls.length).toBeGreaterThan(0));
    const opts = lottieCalls[0] as { renderer: string; autoplay: boolean; animationData: { assets: { p: string }[] } };
    expect(opts.renderer).toBe("svg");
    expect(opts.autoplay).toBe(true);
    expect(opts.animationData.assets[0]!.p).toBe("");
    const item = (lottieCalls[1] as { item: { pause: () => void; goToAndStop: (f: number, b: boolean) => void } }).item;
    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    expect(item.pause).toHaveBeenCalled();
    fireEvent.change(screen.getByRole("slider", { name: "Frame" }), { target: { value: "30" } });
    expect(item.goToAndStop).toHaveBeenCalledWith(30, true);
  });

  it("draws the last frame still for a tile", async () => {
    const onDrawn = vi.fn();
    render(<LottieRenderer content={anim} still onDrawn={onDrawn} />);
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""));
    const item = (lottieCalls[1] as { item: { goToAndStop: (f: number, b: boolean) => void } }).item;
    expect(item.goToAndStop).toHaveBeenCalledWith(59, true);
    expect((lottieCalls[0] as { autoplay: boolean }).autoplay).toBe(false);
  });

  it("says why a source cannot be played", async () => {
    render(<LottieRenderer content={`{"layers":[]}`} />);
    expect(await screen.findByText("This animation could not be played.")).toBeInTheDocument();
  });
});

import { strFromU8, unzipSync } from "fflate";

/**
 * Lottie animations as the portal plays them (#2308).
 *
 * A Lottie JSON source arrives as text; a dotLottie file is a zip uploaded as
 * a managed resource, holding a manifest, its animations and their images. The
 * player is lottie-web's light build, which has no expression support, so no
 * part of an animation is ever evaluated as script. An image the animation
 * names is kept only when it is inline (a data: URL, or a file inside the
 * archive turned into one), so playing an animation fetches nothing.
 */

/** The most an archive may expand to before it is refused. */
export const MAX_ARCHIVE_BYTES = 64 << 20;

export interface Animation {
  fr: number;
  ip: number;
  op: number;
  layers: unknown[];
  assets?: Record<string, unknown>[];
  fonts?: { list?: Record<string, unknown>[] };
  [key: string]: unknown;
}

/** Parses a Lottie JSON source, or throws naming why it is not one. */
export function parseAnimation(text: string): Animation {
  const anim = JSON.parse(text) as Animation;
  if (!anim || typeof anim.fr !== "number" || typeof anim.op !== "number" || !Array.isArray(anim.layers)) {
    throw new Error("this is not a Lottie animation: it needs fr, ip, op and layers");
  }
  return inlineOnly(anim);
}

/** The animation with every image it does not carry inline, and every font path, removed. */
export function inlineOnly(anim: Animation): Animation {
  const assets = (anim.assets ?? []).map((a) => {
    const p = typeof a.p === "string" ? a.p : undefined;
    if (p === undefined || p.toLowerCase().startsWith("data:image/")) return a;
    return { ...a, p: "", u: "", e: 1 };
  });
  const fonts = anim.fonts?.list
    ? { ...anim.fonts, list: anim.fonts.list.map((f) => ({ ...f, fPath: "" })) }
    : anim.fonts;
  return { ...anim, assets, fonts };
}

const IMAGE_TYPES: Record<string, string> = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", webp: "image/webp", gif: "image/gif", svg: "image/svg+xml" };

/** Reads the first animation out of a dotLottie archive, its images inlined. */
export function readDotLottie(bytes: Uint8Array): Animation {
  let total = 0;
  const files = unzipSync(bytes, {
    filter: (f) => {
      total += f.originalSize;
      if (total > MAX_ARCHIVE_BYTES) throw new Error("the dotLottie file expands past what the portal plays");
      return true;
    },
  });
  const manifestBytes = files["manifest.json"];
  if (!manifestBytes) throw new Error("the dotLottie file has no manifest");
  const manifest = JSON.parse(strFromU8(manifestBytes)) as { animations?: { id?: string }[] };
  const id = manifest.animations?.[0]?.id;
  if (!id) throw new Error("the dotLottie manifest names no animation");
  const animBytes = files[`animations/${id}.json`] ?? files[`a/${id}.json`];
  if (!animBytes) throw new Error(`the dotLottie file has no animation ${id}`);
  const anim = JSON.parse(strFromU8(animBytes)) as Animation;
  const assets = (anim.assets ?? []).map((a) => {
    const p = typeof a.p === "string" ? a.p : "";
    if (!p || p.startsWith("data:")) return a;
    const data = files[`images/${p}`] ?? files[`i/${p}`];
    const type = IMAGE_TYPES[p.split(".").pop()?.toLowerCase() ?? ""];
    if (!data || !type) return a;
    return { ...a, u: "", p: `data:${type};base64,${toBase64(data)}`, e: 1 };
  });
  return parseAnimation(JSON.stringify({ ...anim, assets }));
}

function toBase64(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s);
}

import type { ProducedTargetKind } from "@/api/portal/hooks/producers";

// sectionOf is the library a produced file lives in, which is the segment of
// its address on both the portal and the admin console: the two surfaces hold
// the same file under different prefixes and the same section.
export function sectionOf(kind: ProducedTargetKind): "assets" | "collections" | "resources" {
  switch (kind) {
    case "asset":
      return "assets";
    case "collection":
      return "collections";
    default:
      return "resources";
  }
}

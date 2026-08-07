import type { SegmentedOption } from "@/components/patterns/SegmentedControl";

/** How prominently a collection shows the assets it holds. */
export type ThumbSize = "large" | "medium" | "small" | "none";

interface ThumbSizeConfig {
  /** The tile's thumbnail shape, or null for a title-only row. */
  aspect: string | null;
  /** The columns the grid breaks into as the viewport widens. */
  grid: string;
  label: string;
  /** The one- or two-character face on the size switch. */
  short: string;
}

export const THUMB_SIZES: Record<ThumbSize, ThumbSizeConfig> = {
  large: {
    aspect: "aspect-[4/3]",
    grid: "grid-cols-1 md:grid-cols-2 lg:grid-cols-3",
    label: "Large",
    short: "L",
  },
  medium: {
    aspect: "aspect-[3/2] max-h-32",
    grid: "grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4",
    label: "Medium",
    short: "M",
  },
  small: {
    aspect: "aspect-[2/1] max-h-20",
    grid: "grid-cols-1 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5",
    label: "Small",
    short: "S",
  },
  none: {
    aspect: null,
    grid: "grid-cols-1 md:grid-cols-2 lg:grid-cols-3",
    label: "None",
    short: "Off",
  },
};

const ORDER: ThumbSize[] = ["large", "medium", "small", "none"];

export const THUMB_SIZE_OPTIONS: SegmentedOption<ThumbSize>[] = ORDER.map((value) => ({
  value,
  label: `${THUMB_SIZES[value].label} thumbnails`,
  text: THUMB_SIZES[value].short,
}));

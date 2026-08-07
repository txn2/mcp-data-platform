// Allow versus deny is the persona editor's one colour axis, and it is read on
// five surfaces: the pattern editors, the resolved item rows, the resolution
// trace steps, and the hover highlight that links a rule to the items it
// matches. The two palettes are named once here so a change lands on all of
// them, and so no component restates a class list to say "this is the allow
// colour".
//
// The tints are emerald/rose rather than the theme's semantic tokens because
// allow/deny is not success/failure: a denied tool is a correct outcome, and
// the pair has to stay legible side by side in one list.
export const BUCKET_TINT = {
  allow: {
    text: "text-emerald-700 dark:text-emerald-400",
    border: "border-emerald-200 dark:border-emerald-900",
    solid: "bg-emerald-600 text-white hover:bg-emerald-700",
    ring: "ring-2 ring-emerald-400",
    edge: "border-l-emerald-500",
    surface:
      "bg-gradient-to-r from-emerald-50/60 to-transparent dark:from-emerald-950/30",
    step: "bg-emerald-100 text-emerald-900 dark:bg-emerald-950/40 dark:text-emerald-300",
    icon: "text-emerald-600 dark:text-emerald-400",
    action:
      "text-emerald-700 hover:bg-emerald-100 dark:text-emerald-400 dark:hover:bg-emerald-950/60",
  },
  deny: {
    text: "text-rose-700 dark:text-rose-400",
    border: "border-rose-200 dark:border-rose-900",
    solid: "bg-rose-600 text-white hover:bg-rose-700",
    ring: "ring-2 ring-rose-400",
    edge: "border-l-rose-500",
    surface:
      "bg-gradient-to-r from-rose-50/60 to-transparent dark:from-rose-950/30",
    step: "bg-rose-100 text-rose-900 dark:bg-rose-950/40 dark:text-rose-300",
    icon: "text-rose-600 dark:text-rose-400",
    action:
      "text-rose-700 hover:bg-rose-100 dark:text-rose-400 dark:hover:bg-rose-950/60",
  },
} as const;

export type Bucket = keyof typeof BUCKET_TINT;

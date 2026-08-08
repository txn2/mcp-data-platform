import { Button } from "@/components/ui/button";

// FilterChip is the shared pill toggle used for source and tag filters across
// the Knowledge hub (search source chips, knowledge-page tag facet). Keeping one
// component means the pressed style and the aria-pressed semantics live in one
// place rather than being hand-rolled per surface.
//
// It is a `ui/button` toggle rather than a `ui/badge`: what it renders is a
// control the reader presses, not a status the row carries. Only the rounding
// is its own, which is what makes a facet read as a chip.
//
// The pressed face is the filled primary one rather than `secondary`, because
// these chips sit in rows of eight: `secondary` on `card` is a five-percent
// lightness step with no hue, which states which facets are on only to someone
// comparing them side by side.
export function FilterChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count?: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      variant={active ? "default" : "outline"}
      size="xs"
      onClick={onClick}
      aria-pressed={active}
      className="rounded-full"
    >
      {label}
      {count != null && <span className="opacity-60">{count}</span>}
    </Button>
  );
}

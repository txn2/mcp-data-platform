// FilterChip is the shared pill toggle used for source and tag filters across
// the Knowledge hub (search source chips, knowledge-page tag facet). Keeping one
// component means the active style and the aria-pressed semantics live in one
// place rather than being hand-rolled per surface.
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
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`rounded-full border px-2.5 py-1 text-xs font-medium transition-colors ${
        active
          ? "border-primary bg-primary/10 text-primary"
          : "border-border text-muted-foreground hover:bg-muted"
      }`}
    >
      {label}
      {count != null && <span className="ml-1 opacity-60">{count}</span>}
    </button>
  );
}

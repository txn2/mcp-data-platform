// SubTabBar renders the secondary navigation inside the Knowledge and Insights
// tabs as a segmented (pill) control, visually subordinate to the primary
// underline tabs above it so the two levels read as a hierarchy rather than two
// identical bars. An optional badge surfaces a count (e.g. pending reviews).
//
// `dense` is the third level: the Catalog section's own inner tabs (#1194) sit
// below a pill bar that is already a sub-tab bar, so they render smaller and
// borderless. Three identically weighted bars would flatten the hierarchy the
// two sizes above exist to express.
export function SubTabBar<T extends string>({
  tabs,
  active,
  onSelect,
  dense = false,
}: {
  tabs: { key: T; label: string; badge?: number }[];
  active: T;
  onSelect: (key: T) => void;
  dense?: boolean;
}) {
  return (
    <div
      className={
        dense
          ? "inline-flex flex-wrap items-center gap-1 rounded-md bg-muted/40 p-0.5"
          : "inline-flex flex-wrap items-center gap-1 rounded-lg border bg-muted/40 p-1"
      }
    >
      {tabs.map((t) => {
        const isActive = active === t.key;
        return (
          <button
            key={t.key}
            onClick={() => onSelect(t.key)}
            className={`flex items-center gap-2 rounded-md font-medium transition-colors ${
              dense ? "px-2.5 py-1 text-xs" : "px-3 py-1.5 text-sm"
            } ${
              isActive
                ? "bg-background text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
            {t.badge != null && t.badge > 0 && (
              <span
                className={`rounded-full px-1.5 text-[11px] font-semibold ${
                  isActive
                    ? "bg-primary/15 text-primary"
                    : "bg-muted-foreground/15 text-muted-foreground"
                }`}
              >
                {t.badge}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

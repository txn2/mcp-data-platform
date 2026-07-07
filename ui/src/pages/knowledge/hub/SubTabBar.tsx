// SubTabBar renders the secondary navigation inside the Knowledge and Insights
// tabs as a segmented (pill) control, visually subordinate to the primary
// underline tabs above it so the two levels read as a hierarchy rather than two
// identical bars. An optional badge surfaces a count (e.g. pending reviews).
export function SubTabBar<T extends string>({
  tabs,
  active,
  onSelect,
}: {
  tabs: { key: T; label: string; badge?: number }[];
  active: T;
  onSelect: (key: T) => void;
}) {
  return (
    <div className="inline-flex flex-wrap items-center gap-1 rounded-lg border bg-muted/40 p-1">
      {tabs.map((t) => {
        const isActive = active === t.key;
        return (
          <button
            key={t.key}
            onClick={() => onSelect(t.key)}
            className={`flex items-center gap-2 rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
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

import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

// SubTabBar renders the secondary navigation inside the Knowledge and Insights
// tabs as a segmented (pill) control on the shared Tabs primitive, visually
// subordinate to the primary underline tabs above it so the two levels read as
// a hierarchy rather than two identical bars. An optional badge surfaces a
// count (e.g. pending reviews).
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
    <Tabs value={active} onValueChange={(v) => onSelect(v as T)}>
      <TabsList
        className={cn(
          "group-data-[orientation=horizontal]/tabs:h-auto flex-wrap justify-start",
          dense ? "rounded-md bg-muted/40 p-0.5" : "rounded-lg border bg-muted/40 p-1",
        )}
      >
        {tabs.map((t) => (
          <TabsTrigger
            key={t.key}
            value={t.key}
            className={cn("flex-none", dense ? "px-2.5 py-1 text-xs" : "px-3 py-1.5 text-sm")}
          >
            {t.label}
            {t.badge != null && t.badge > 0 && (
              <span
                className={cn(
                  "rounded-full px-1.5 text-[11px] font-semibold",
                  active === t.key
                    ? "bg-primary/15 text-primary"
                    : "bg-muted-foreground/15 text-muted-foreground",
                )}
              >
                {t.badge}
              </span>
            )}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}

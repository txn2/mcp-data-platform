import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";

/** Ownership scope for asset/collection listings. */
export type Scope = "mine" | "shared" | "all";

const SCOPE_STORAGE_KEY = "asset-scope";

/**
 * Read the persisted scope. Shared by the Assets and Collections tabs so the
 * choice carries across them. Defensive against environments without
 * localStorage (jsdom/SSR); defaults to "all" so everything available to the
 * user is visible at a glance.
 */
export function getStoredScope(): Scope {
  try {
    const s = globalThis.localStorage?.getItem(SCOPE_STORAGE_KEY);
    return s === "shared" || s === "mine" ? s : "all";
  } catch {
    return "all";
  }
}

export function storeScope(scope: Scope) {
  try {
    globalThis.localStorage?.setItem(SCOPE_STORAGE_KEY, scope);
  } catch {
    /* persistence is best-effort */
  }
}

const OPTIONS: { value: Scope; label: string }[] = [
  { value: "mine", label: "Mine" },
  { value: "shared", label: "Shared" },
  { value: "all", label: "All" },
];

interface Props {
  value: Scope;
  onChange: (scope: Scope) => void;
}

/**
 * Segmented Mine / Shared / All control.
 *
 * A `ui/tabs` list rather than a `SegmentedControl`: what the choice switches
 * between is three different listings of the same page, which is a tablist, and
 * it keeps the `role="tab"` semantics the screenshot suite drives it by.
 */
export function ScopeFilter({ value, onChange }: Props) {
  return (
    <Tabs
      value={value}
      // Manual activation: each scope is a different query, so Radix's
      // select-on-focus default would fire one per face an arrow key passes.
      activationMode="manual"
      onValueChange={(v) => onChange(v as Scope)}
    >
      <TabsList aria-label="Ownership scope">
        {OPTIONS.map((opt) => (
          <TabsTrigger
            key={opt.value}
            value={opt.value}
            type="button"
            // The listing these faces choose between is the page below, not a
            // TabsContent, so Radix's stamped panel id names nothing.
            aria-controls={undefined}
          >
            {opt.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  );
}

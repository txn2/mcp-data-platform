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

/** Ownership scope for the scripts listing, which has no shared face. */
export type ScriptScope = "mine" | "all";

const SCRIPT_SCOPE_STORAGE_KEY = "script-scope";

/**
 * Read the persisted scripts scope. Its own key, not the assets one: a reader
 * who browses every asset has said nothing about whose scripts they want, and
 * the scripts listing defaults to their own (#1795).
 */
export function getStoredScriptScope(): ScriptScope {
  try {
    return globalThis.localStorage?.getItem(SCRIPT_SCOPE_STORAGE_KEY) === "all" ? "all" : "mine";
  } catch {
    return "mine";
  }
}

export function storeScriptScope(scope: ScriptScope) {
  try {
    globalThis.localStorage?.setItem(SCRIPT_SCOPE_STORAGE_KEY, scope);
  } catch {
    /* persistence is best-effort */
  }
}

/** The two faces the scripts listing offers. */
export const SCRIPT_SCOPE_OPTIONS: { value: ScriptScope; label: string }[] = [
  { value: "mine", label: "Mine" },
  { value: "all", label: "All" },
];

interface Props<T extends string> {
  value: T;
  onChange: (scope: T) => void;
  // The faces to offer. Defaults to the asset scopes, which is what every
  // caller wanted until scripts needed Mine/All (#1795).
  options?: { value: T; label: string }[];
  // The control's accessible name, which says what the faces choose between.
  label?: string;
}

/**
 * Segmented Mine / Shared / All control.
 *
 * A `ui/tabs` list rather than a `SegmentedControl`: what the choice switches
 * between is three different listings of the same page, which is a tablist, and
 * it keeps the `role="tab"` semantics the screenshot suite drives it by.
 */
export function ScopeFilter<T extends string = Scope>({
  value,
  onChange,
  options = OPTIONS as { value: T; label: string }[],
  label = "Ownership scope",
}: Props<T>) {
  return (
    <Tabs
      value={value}
      // Manual activation: each scope is a different query, so Radix's
      // select-on-focus default would fire one per face an arrow key passes.
      activationMode="manual"
      onValueChange={(v) => onChange(v as T)}
    >
      <TabsList aria-label={label}>
        {options.map((opt) => (
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

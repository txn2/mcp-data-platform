import { cn } from "@/lib/utils";

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

/** Segmented Mine / Shared / All control. */
export function ScopeFilter({ value, onChange }: Props) {
  return (
    <div className="flex gap-0.5 rounded-md border p-0.5" role="tablist" aria-label="Ownership scope">
      {OPTIONS.map((opt) => (
        <button
          key={opt.value}
          type="button"
          role="tab"
          aria-selected={value === opt.value}
          onClick={() => onChange(opt.value)}
          className={cn(
            "rounded-sm px-3 py-1.5 text-sm font-medium transition-colors",
            value === opt.value ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground",
          )}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

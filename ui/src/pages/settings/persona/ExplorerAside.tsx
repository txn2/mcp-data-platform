import { TemplateRow } from "./primitives";
import { ApiRouteTrace } from "./ApiRouteTrace";
import { Trace } from "./Trace";
import type { RouteFocus } from "./types";
import type { Resolution } from "./resolve";
import { POSITIVE_LABEL, type ExplorerCounts } from "./ExplorerToolbar";
import type { Item, PersonaDraft, Scope } from "./types";

// QUICK_TEMPLATES are the four persona shapes an operator reaches for often
// enough to be worth one click. Each one replaces both tool buckets outright,
// so applying a template is a starting point rather than an accumulation.
const QUICK_TEMPLATES: {
  name: string;
  hint: string;
  allowTools: string[];
  denyTools: string[];
}[] = [
  { name: "Administrator", hint: "Allow everything", allowTools: ["*"], denyTools: [] },
  {
    name: "Read Only",
    hint: "search, browse, get_*, list_*",
    allowTools: ["*_search", "*_browse", "*_get_*", "*_list_*", "*_describe_*"],
    denyTools: [],
  },
  {
    name: "Analyst",
    hint: "Query + catalog, no mutations",
    allowTools: ["trino_*", "datahub_*", "s3_get_*", "s3_list_*"],
    denyTools: ["*_delete_*", "*_execute"],
  },
  {
    name: "Engineer",
    hint: "Everything except destructive",
    allowTools: ["*"],
    denyTools: ["*_delete_*"],
  },
];

// ExplorerAside is the permissions preview's right rail: the allow/deny tally
// with its share bar, the resolution trace for whatever row the operator is
// pointing at, and the quick templates that seed the tool buckets.
export function ExplorerAside({
  counts,
  focusItem,
  focusResolution,
  focusItemMeta,
  allowList,
  denyList,
  scope,
  isReadOnly,
  onUpdate,
  routeFocus,
}: {
  counts: ExplorerCounts;
  focusItem: string | null;
  focusResolution: Resolution | null | undefined;
  focusItemMeta: Item | null | undefined;
  allowList: string[];
  denyList: string[];
  scope: Scope;
  isReadOnly: boolean;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  /** The operation the pointer is on in the API-endpoint scope, if any. */
  routeFocus: RouteFocus | null;
}) {
  return (
    <aside className="flex flex-col border-t xl:overflow-y-auto xl:border-l xl:border-t-0">
      <div className="grid grid-cols-2 border-b">
        <div className="border-r px-4 py-3">
          <div className="text-2xl font-semibold text-emerald-600 dark:text-emerald-400">
            {counts.allowed}
          </div>
          <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
            {POSITIVE_LABEL[scope]}
          </div>
        </div>
        <div className="px-4 py-3">
          <div className="text-2xl font-semibold text-rose-600 dark:text-rose-400">
            {counts.denied}
          </div>
          <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
            denied
          </div>
        </div>
        <div className="col-span-2 px-4 pb-3">
          <div className="h-1.5 overflow-hidden rounded-full bg-muted">
            <div
              className="h-full bg-emerald-500 transition-all"
              style={{
                width:
                  counts.total === 0
                    ? "0%"
                    : `${(counts.allowed / counts.total) * 100}%`,
              }}
            />
          </div>
        </div>
      </div>

      <div className="border-b p-4">
        <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          Resolution Trace
        </div>
        {scope === "api" ? (
          routeFocus ? (
            <ApiRouteTrace
              connection={routeFocus.connection}
              method={routeFocus.method}
              path={routeFocus.path}
              resolution={routeFocus.resolution}
            />
          ) : (
            <p className="py-4 text-center text-[11px] text-muted-foreground">
              Hover or click an operation to trace its decision.
            </p>
          )
        ) : !focusItem || !focusResolution ? (
          <p className="py-4 text-center text-[11px] text-muted-foreground">
            Hover or click an item to trace its decision.
          </p>
        ) : (
          <Trace
            name={focusItem}
            meta={focusItemMeta}
            resolution={focusResolution}
            hasAllow={allowList.length > 0}
            hasDeny={denyList.length > 0}
          />
        )}
      </div>

      {scope === "tools" && (
        <fieldset disabled={isReadOnly} className="block p-4">
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
            Quick Templates
          </div>
          <div className="space-y-1">
            {QUICK_TEMPLATES.map((t) => (
              <TemplateRow
                key={t.name}
                name={t.name}
                hint={t.hint}
                onApply={() =>
                  onUpdate({ allowTools: t.allowTools, denyTools: t.denyTools })
                }
              />
            ))}
          </div>
        </fieldset>
      )}
    </aside>
  );
}

import { useEffect, useMemo, useState } from "react";
import { useTools } from "@/api/admin/hooks";
import { ToolsList } from "./ToolsList";
import { ToolDetail, type ToolDetailTab } from "./ToolDetail";

const VALID_TABS: ToolDetailTab[] = [
  "overview",
  "tryit",
  "activity",
  "enrichment",
  "visibility",
];

function isToolDetailTab(v: string): v is ToolDetailTab {
  return (VALID_TABS as string[]).includes(v);
}

// Read selection + tab from URL search params on first mount, then keep URL
// in sync via replaceState so refresh and back-button work.
function readSelectionFromURL(): {
  selected: string | null;
  tab: ToolDetailTab;
} {
  if (typeof window === "undefined") {
    return { selected: null, tab: "overview" };
  }
  const params = new URLSearchParams(window.location.search);
  const selected = params.get("selected");
  const tabParam = params.get("tab");
  const tab = tabParam && isToolDetailTab(tabParam) ? tabParam : "overview";
  return { selected, tab };
}

export function ToolsPage({ initialTab: _initialTab }: { initialTab?: string } = {}) {
  // The AppShell passes hash-based initialTab for legacy pages; we use
  // search params here instead so they don't collide with tool selection.
  void _initialTab;

  const initial = useMemo(readSelectionFromURL, []);
  const [selected, setSelected] = useState<string | null>(initial.selected);
  const [tab, setTab] = useState<ToolDetailTab>(initial.tab);

  const { data: toolsData, isLoading: toolsLoading } = useTools();

  const tools = toolsData?.tools ?? [];
  const hiddenToolNames = useMemo(
    () => new Set(tools.filter((t) => t.hidden).map((t) => t.name)),
    [tools],
  );

  // Auto-select the first tool once the list arrives, when no URL selection.
  useEffect(() => {
    if (!selected && tools.length > 0) {
      setSelected(tools[0]!.name);
    }
  }, [selected, tools]);

  // Validate URL-supplied selection: clear if the tool no longer exists.
  useEffect(() => {
    if (!selected || tools.length === 0) return;
    if (!tools.some((t) => t.name === selected)) {
      setSelected(tools[0]?.name ?? null);
    }
  }, [selected, tools]);

  // Reflect selection + tab in URL for shareable links and back/forward.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const params = new URLSearchParams(window.location.search);
    if (selected) {
      params.set("selected", selected);
    } else {
      params.delete("selected");
    }
    if (tab !== "overview") {
      params.set("tab", tab);
    } else {
      params.delete("tab");
    }
    const qs = params.toString();
    const next =
      window.location.pathname +
      (qs ? `?${qs}` : "") +
      window.location.hash;
    window.history.replaceState(null, "", next);
  }, [selected, tab]);

  return (
    <div className="flex h-[calc(100vh-8rem)] gap-3 overflow-hidden rounded-lg border bg-card">
      <aside className="w-72 shrink-0 border-r">
        {toolsLoading ? (
          <div className="p-6 text-center text-sm text-muted-foreground">
            Loading tools…
          </div>
        ) : (
          <ToolsList
            tools={tools}
            hiddenToolNames={hiddenToolNames}
            selected={selected}
            onSelect={setSelected}
          />
        )}
      </aside>

      <section className="flex-1 overflow-hidden">
        {selected ? (
          <ToolDetail toolName={selected} tab={tab} onTabChange={setTab} />
        ) : (
          <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
            Select a tool from the list.
          </div>
        )}
      </section>
    </div>
  );
}

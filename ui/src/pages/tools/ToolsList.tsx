import { useMemo, useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import type { ToolInfo } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { formatToolName } from "@/lib/formatToolName";
import { cn } from "@/lib/utils";

interface ToolsListProps {
  tools: ToolInfo[];
  hiddenToolNames: Set<string>;
  selected: string | null;
  onSelect: (toolName: string) => void;
}

type GroupBy = "connection" | "kind";

export function ToolsList({
  tools,
  hiddenToolNames,
  selected,
  onSelect,
}: ToolsListProps) {
  const [search, setSearch] = useState("");
  const [groupBy, setGroupBy] = useState<GroupBy>("connection");

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return tools;
    return tools.filter(
      (t) =>
        t.name.toLowerCase().includes(q) ||
        (t.title ?? "").toLowerCase().includes(q),
    );
  }, [tools, search]);

  const groups = useMemo(() => {
    const map = new Map<string, ToolInfo[]>();
    for (const t of filtered) {
      const key =
        groupBy === "connection"
          ? t.connection || "platform"
          : t.kind || "other";
      const list = map.get(key) ?? [];
      list.push(t);
      map.set(key, list);
    }
    for (const list of map.values()) {
      list.sort((a, b) => a.name.localeCompare(b.name));
    }
    return [...map.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [filtered, groupBy]);

  return (
    <div className="flex h-full flex-col">
      <div className="space-y-2 border-b p-3">
        <SearchInput
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search tools…"
          aria-label="Search tools"
        />
        <div className="flex items-center gap-2 text-xs">
          <span className="text-muted-foreground">Group by</span>
          <SegmentedControl
            label="Group tools by"
            value={groupBy}
            onChange={setGroupBy}
            options={[
              { value: "connection", label: "Group by connection", text: "connection" },
              { value: "kind", label: "Group by kind", text: "kind" },
            ]}
          />
        </div>
      </div>

      <div className="flex-1 overflow-auto">
        {groups.length === 0 ? (
          <EmptyState className="m-3">
            {search ? "No tools match." : "No tools available."}
          </EmptyState>
        ) : (
          groups.map(([groupName, items]) => (
            <div key={groupName}>
              <div className="sticky top-0 z-10 border-b bg-muted px-3 py-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                {groupName}{" "}
                <span className="ml-1 text-muted-foreground/70">
                  ({items.length})
                </span>
              </div>
              {items.map((t) => {
                const isHidden = hiddenToolNames.has(t.name);
                const isSelected = t.name === selected;
                return (
                  <button
                    key={`${t.name}-${t.connection}`}
                    onClick={() => onSelect(t.name)}
                    className={cn(
                      "flex w-full items-center gap-2 border-b px-3 py-2 text-left text-sm transition-colors hover:bg-muted/40",
                      isSelected && "bg-primary/10 hover:bg-primary/15",
                    )}
                  >
                    {isHidden ? (
                      <EyeOff className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60" />
                    ) : (
                      <Eye className="h-3.5 w-3.5 shrink-0 text-muted-foreground/40" />
                    )}
                    <div className="min-w-0 flex-1">
                      <div
                        className={cn(
                          "truncate",
                          isHidden && "text-muted-foreground",
                        )}
                      >
                        {formatToolName(t.name, t.title)}
                      </div>
                      <div className="truncate text-[11px] text-muted-foreground">
                        {t.kind}
                      </div>
                    </div>
                  </button>
                );
              })}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

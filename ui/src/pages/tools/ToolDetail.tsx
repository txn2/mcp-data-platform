import { EyeOff, Loader2 } from "lucide-react";
import { useToolDetail } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { formatToolName } from "@/lib/formatToolName";
import { cn } from "@/lib/utils";
import type { ToolDetail as ToolDetailDTO } from "@/api/admin/types";
import { OverviewTab } from "./tabs/OverviewTab";
import { TryItTab } from "./tabs/TryItTab";
import { ActivityTab } from "./tabs/ActivityTab";
import { EnrichmentTab } from "./tabs/EnrichmentTab";
import { VisibilityTab } from "./tabs/VisibilityTab";

export type ToolDetailTab =
  | "overview"
  | "tryit"
  | "activity"
  | "enrichment"
  | "visibility";

const TAB_LABELS: Record<ToolDetailTab, string> = {
  overview: "Overview",
  tryit: "Try It",
  activity: "Activity",
  enrichment: "Enrichment",
  visibility: "Visibility",
};

interface ToolDetailProps {
  toolName: string;
  tab: ToolDetailTab;
  onTabChange: (tab: ToolDetailTab) => void;
}

export function ToolDetail({ toolName, tab, onTabChange }: ToolDetailProps) {
  const { data, isLoading, error } = useToolDetail(toolName);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center text-muted-foreground">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        Loading…
      </div>
    );
  }
  if (error || !data) {
    return (
      <div className="p-6 text-sm text-destructive">
        Failed to load tool detail: {error?.message ?? "unknown error"}
      </div>
    );
  }

  // Enrichment tab is only meaningful for gateway-proxied tools.
  const isGatewayProxied = data.toolkit_kind === "mcp" && !!data.connection;
  const tabs: ToolDetailTab[] = isGatewayProxied
    ? ["overview", "tryit", "activity", "enrichment", "visibility"]
    : ["overview", "tryit", "activity", "visibility"];

  // Defensive: if URL points at a tab that's not visible for this tool kind,
  // fall back to overview rendering (the parent's URL state is left as-is so
  // the user keeps their query string when switching to a kind that supports it).
  const effectiveTab: ToolDetailTab = tabs.includes(tab) ? tab : "overview";

  return (
    <div className="flex h-full flex-col">
      <div className="border-b p-4">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-base font-semibold">
            {formatToolName(data.name, data.title)}
          </h2>
          <StatusBadge variant="neutral">{data.toolkit_kind}</StatusBadge>
          {data.connection && (
            <StatusBadge variant="neutral">{data.connection}</StatusBadge>
          )}
          {data.hidden_by_global_deny && (
            <span className="inline-flex items-center gap-1 rounded-full bg-yellow-100 px-2 py-0.5 text-xs font-medium text-yellow-800">
              <EyeOff className="h-3 w-3" />
              hidden globally
              {data.global_deny_pattern && (
                <span className="opacity-70">({data.global_deny_pattern})</span>
              )}
            </span>
          )}
        </div>
      </div>

      <div className="flex border-b">
        {tabs.map((t) => (
          <button
            key={t}
            onClick={() => onTabChange(t)}
            className={cn(
              "px-4 py-2 text-sm font-medium transition-colors",
              effectiveTab === t
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {TAB_LABELS[t]}
          </button>
        ))}
      </div>

      <div className="flex-1 overflow-auto p-4">
        <ToolDetailTabBody detail={data} tab={effectiveTab} />
      </div>
    </div>
  );
}

function ToolDetailTabBody({
  detail,
  tab,
}: {
  detail: ToolDetailDTO;
  tab: ToolDetailTab;
}) {
  switch (tab) {
    case "overview":
      return <OverviewTab detail={detail} />;
    case "tryit":
      return <TryItTab detail={detail} />;
    case "activity":
      return <ActivityTab detail={detail} />;
    case "enrichment":
      return <EnrichmentTab detail={detail} />;
    case "visibility":
      return <VisibilityTab detail={detail} />;
  }
}

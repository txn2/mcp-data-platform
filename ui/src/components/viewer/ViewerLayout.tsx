import { useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

interface ViewerLayoutProps {
  /** Where the reader came from, offered as the header's Back link. */
  onBack: () => void;
  /** What this record is called. */
  title: ReactNode;
  /** The identity line under the title: where the record sits, and what file
   * it holds. */
  subtitle?: ReactNode;
  /** The record's own actions, placed left of the sidebar toggle. */
  actions?: ReactNode;
  /** Everything about the record that is not its content. Omit it and neither
   * the column nor the toggle that reveals it is rendered. */
  sidebar?: ReactNode;
  /** Whether the sidebar is showing when the page opens. */
  sidebarInitiallyOpen?: boolean;
  /** The record's content, at the full width the sidebar leaves it. */
  children: ReactNode;
}

/**
 * The shape a stored file opens in, written once for the two kinds that have
 * one: a portal asset and a managed resource (#1470).
 *
 * Both are a file with a content type, a version trail and a table
 * registration, and until this existed they were drawn differently -- an asset
 * full width at a route of its own, a resource in a 32rem dialog with its
 * preview capped at half the viewport inside a second scrolling column. What
 * the two share is this: the content takes the page, the metadata sits beside
 * it rather than above it, and the page area is the only thing that scrolls.
 *
 * Only the layout, the header and whether the sidebar is showing live here.
 * The actions and both columns' contents are the kind's own and are passed in,
 * which is what keeps this from growing an asset's share, copy and revert.
 */
export function ViewerLayout({
  onBack,
  title,
  subtitle,
  actions,
  sidebar,
  sidebarInitiallyOpen = false,
  children,
}: ViewerLayoutProps) {
  const [sidebarOpen, setSidebarOpen] = useState(sidebarInitiallyOpen);

  return (
    <div className="flex h-full gap-4">
      <div className="min-w-0 flex-1 space-y-3">
        <PageHeader
          onBack={onBack}
          title={<span className="min-w-0 truncate">{title}</span>}
          subtitle={subtitle}
          actions={
            <>
              {actions}
              {sidebar && (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => setSidebarOpen(!sidebarOpen)}
                  title={sidebarOpen ? "Hide details" : "Show details"}
                >
                  {sidebarOpen ? <ChevronRight /> : <ChevronLeft />}
                </Button>
              )}
            </>
          }
        />
        {children}
      </div>

      {sidebar && sidebarOpen && (
        <Card className="w-80 shrink-0 gap-4 overflow-auto p-4">{sidebar}</Card>
      )}
    </div>
  );
}

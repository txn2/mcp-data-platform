import { useState, type ReactNode } from "react";
import { ArrowLeft, ChevronLeft, ChevronRight, Info } from "lucide-react";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useIsMobile } from "@/hooks/useIsMobile";

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
  /** The sidebar held by the caller, which a phone opens from its More menu
   * (#2601); left out, the layout holds it. */
  sidebarOpen?: boolean;
  onSidebarOpenChange?: (open: boolean) => void;
  /** On a phone, the compact title block: a back arrow beside a title of up to
   * two lines and a quiet line under it, with no actions row; the caller puts
   * its actions on the content's toolbar and the details toggle in More
   * (#2601). */
  phoneHeader?: boolean;
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
  sidebarOpen: heldOpen,
  onSidebarOpenChange,
  phoneHeader = false,
  children,
}: ViewerLayoutProps) {
  const [ownOpen, setOwnOpen] = useState(sidebarInitiallyOpen);
  const sidebarOpen = heldOpen ?? ownOpen;
  const setSidebarOpen = onSidebarOpenChange ?? setOwnOpen;
  // On a phone the details open under the content, not beside it, so the
  // toggle is an information icon rather than an arrow pointing sideways.
  const isMobile = useIsMobile();
  const compact = isMobile && phoneHeader;

  return (
    // Below lg the sidebar is stacked under the content rather than beside it:
    // a 320px column next to the content is wider than a phone screen, and the
    // page scrolled sideways whenever it was open (#1693).
    <div className="flex h-full flex-col gap-4 lg:flex-row">
      {/* A flex column rather than stacked blocks: the content is the column's
          last item, and a viewer that grows (an HTML frame, #1769) takes the
          height left under the header and the controls, so the page fits the
          viewport with its own padding under the content. */}
      <div data-testid="viewer-content" className="flex min-w-0 flex-1 flex-col gap-3">
        {compact ? (
          <PhoneTitle onBack={onBack} title={title} subtitle={subtitle} />
        ) : (
          <DesktopHeader
            onBack={onBack}
            title={title}
            subtitle={subtitle}
            actions={actions}
            toggle={sidebar ? <DetailsToggle open={sidebarOpen} onToggle={() => setSidebarOpen(!sidebarOpen)} phone={isMobile} /> : null}
          />
        )}
        {children}
      </div>

      {sidebar && sidebarOpen && (
        <Card data-testid="viewer-details" className="w-full shrink-0 gap-4 overflow-auto p-4 lg:w-80">
          {sidebar}
        </Card>
      )}
    </div>
  );
}

/** PhoneTitle is a phone's title block: the way back as an arrow beside the
 * title, which takes two lines before it truncates, and one quiet line under it
 * (#2601). */
function PhoneTitle({
  onBack,
  title,
  subtitle,
}: {
  onBack: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
}) {
  return (
    <div className="-ml-2 flex items-start gap-1" data-testid="phone-title">
      <Button
        variant="ghost"
        size="icon-sm"
        onClick={onBack}
        aria-label="Back"
        title="Back"
        className="mt-0.5 shrink-0 text-muted-foreground"
      >
        <ArrowLeft />
      </Button>
      <div className="min-w-0 pt-1">
        <h2 className="line-clamp-2 text-[17px] leading-[22px] font-semibold break-words">
          {title}
        </h2>
        {subtitle && (
          <div className="mt-1 text-xs leading-4 text-muted-foreground">
            {subtitle}
          </div>
        )}
      </div>
    </div>
  );
}

/** DesktopHeader is the page header with the record's actions and the
 * details toggle beside them. */
function DesktopHeader({
  onBack,
  title,
  subtitle,
  actions,
  toggle,
}: {
  onBack: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  toggle: ReactNode;
}) {
  return (
    <PageHeader
      onBack={onBack}
      title={<span className="min-w-0 truncate">{title}</span>}
      subtitle={subtitle}
      actions={
        <>
          {actions}
          {toggle}
        </>
      }
    />
  );
}

/** DetailsToggle shows or hides the details: an arrow beside the content, an
 * information icon on a phone, where they open under it. */
function DetailsToggle({ open, onToggle, phone }: { open: boolean; onToggle: () => void; phone: boolean }) {
  const label = open ? "Hide details" : "Show details";
  return (
    <Button variant="ghost" size="icon-sm" onClick={onToggle} title={label} aria-label={label} aria-pressed={phone ? open : undefined}>
      {phone ? <Info /> : open ? <ChevronRight /> : <ChevronLeft />}
    </Button>
  );
}

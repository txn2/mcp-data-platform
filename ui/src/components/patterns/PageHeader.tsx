import { ArrowLeft, type LucideIcon } from "lucide-react";

// PageHeader is the one shape a detail view opens with: the way back, where the
// entity sits, what it is, and what the reader can do to it — in that order, in
// one place. Page-level actions live in its actions slot so they stop scattering
// down the page; section-scoped actions belong to their SectionCard instead.
export function PageHeader({
  backLabel,
  onBack,
  breadcrumb,
  icon: Icon,
  title,
  urn,
  subtitle,
  actions,
}: {
  backLabel?: string;
  onBack?: () => void;
  // The location trail (e.g. GlossaryBreadcrumb), rendered between the back
  // link and the title.
  breadcrumb?: React.ReactNode;
  icon?: LucideIcon;
  title: React.ReactNode;
  // The entity's identity line under the title, rendered monospace.
  urn?: string;
  // A plain descriptive line under the title (a category, a status).
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <div className="space-y-3">
      {onBack && (
        <button
          onClick={onBack}
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowLeft className="size-4" /> {backLabel ?? "Back"}
        </button>
      )}
      {breadcrumb}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-lg font-semibold">
            {Icon && <Icon className="size-4 shrink-0 text-muted-foreground" />}
            {title}
          </h2>
          {urn && <p className="break-all font-mono text-xs text-muted-foreground">{urn}</p>}
          {subtitle && <p className="text-xs text-muted-foreground">{subtitle}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}

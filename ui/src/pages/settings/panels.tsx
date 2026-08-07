import type { LucideIcon } from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/utils";

// The two card shells the settings area is built from. Both are ui/card with
// the page's own header bar, so every settings surface states its title,
// purpose, and action in the same place — and the banners that qualify a
// section always sit in the same two slots relative to that bar.

function HeaderBar({
  icon: Icon,
  title,
  description,
  action,
}: {
  icon?: LucideIcon;
  title: string;
  description?: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-4 border-b px-5 py-3">
      <div className="flex min-w-0 items-center gap-3">
        {Icon && <Icon className="size-4 shrink-0 text-muted-foreground" />}
        <div className="min-w-0">
          <h3 className="text-sm font-semibold leading-none">{title}</h3>
          {description && (
            <p className="mt-1 text-xs text-muted-foreground">{description}</p>
          )}
        </div>
      </div>
      {action && <div className="flex shrink-0 items-center gap-2">{action}</div>}
    </div>
  );
}

// SettingsCard is a settings section that sizes to its content: the SMTP
// server, the review-queue alert, a user's notification preferences.
//
// `notices` states why the section cannot act and renders above the header, so
// the qualification is read before the controls it qualifies. `feedback`
// reports the outcome of the last save and renders under the header, next to
// the button that caused it.
export function SettingsCard({
  icon,
  title,
  description,
  action,
  notices,
  feedback,
  contentClassName,
  children,
}: {
  icon?: LucideIcon;
  title: string;
  description?: React.ReactNode;
  action?: React.ReactNode;
  notices?: React.ReactNode;
  feedback?: React.ReactNode;
  contentClassName?: string;
  children: React.ReactNode;
}) {
  return (
    <Card className="gap-0 overflow-hidden py-0">
      {notices}
      <HeaderBar
        icon={icon}
        title={title}
        description={description}
        action={action}
      />
      {feedback}
      <CardContent className={cn("p-5", contentClassName)}>{children}</CardContent>
    </Card>
  );
}

// PanelShell is a settings surface that owns the viewport instead of sizing to
// its content — the key, user, and changelog lists, whose body scrolls under a
// pinned header. Its children are laid out directly rather than through
// CardContent so a table can run edge to edge.
export function PanelShell({
  icon,
  title,
  description,
  action,
  notices,
  children,
}: {
  icon?: LucideIcon;
  title: string;
  description?: React.ReactNode;
  action?: React.ReactNode;
  notices?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card className="flex h-[calc(100vh-8rem)] flex-col gap-0 overflow-hidden py-0">
      {notices}
      <HeaderBar
        icon={icon}
        title={title}
        description={description}
        action={action}
      />
      {children}
    </Card>
  );
}

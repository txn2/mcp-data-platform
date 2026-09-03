import { Save, Check, AlertCircle, RefreshCw, XCircle } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// Chrome shared by the cards on the platform settings page: the banners that
// explain why a section cannot act, the save button, and the last-write meta.
// Every section states those things the same way, so they are written once
// here rather than per card.

// A banner spans the full width of the card it sits in, so it drops the
// Alert's own rounding and side borders and keeps only the rule that separates
// it from the row below.
const bannerClass = "rounded-none border-x-0 border-t-0 px-5 py-2.5 text-xs";

export function ErrorBanner({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  return (
    <Alert variant="destructive" className={bannerClass}>
      <XCircle />
      {/* AlertDescription restates text-sm, so a banner that wants the
          settings area's smaller scale has to say so on the description
          itself, not only on the Alert. */}
      <AlertDescription className="flex w-full flex-row items-center gap-2 text-xs">
        <span className="flex-1">{message}</span>
        {onRetry && (
          <Button type="button" variant="ghost" size="xs" onClick={onRetry}>
            <RefreshCw />
            Retry
          </Button>
        )}
      </AlertDescription>
    </Alert>
  );
}

// WarningBanner states a condition that is saved and accepted but leaves the
// section unable to do its job. It never blocks an edit.
export function WarningBanner({
  children,
  className,
  ...props
}: React.ComponentProps<typeof Alert>) {
  return (
    <Alert variant="warning" className={cn(bannerClass, className)} {...props}>
      <AlertCircle />
      <AlertDescription className="text-xs">{children}</AlertDescription>
    </Alert>
  );
}

// ReadOnlyBanner explains why the section cannot be edited, and what to change
// to make it editable -- never a bare refusal.
export function ReadOnlyBanner() {
  return (
    <WarningBanner>
      Configuration is read-only: no database configured. Set{" "}
      <code className="font-mono">database.dsn</code> to enable editing.
    </WarningBanner>
  );
}

export function SaveButton({
  dirty,
  saving,
  saveSuccess,
  onSave,
  disabled,
}: {
  dirty: boolean;
  saving: boolean;
  saveSuccess: boolean;
  onSave: () => void;
  // disabled blocks a save the server would refuse anyway, so the refusal is
  // visible on the button rather than arriving as an error after the click.
  disabled?: boolean;
}) {
  return (
    <Button
      type="button"
      size="sm"
      onClick={onSave}
      disabled={!dirty || saving || disabled}
      className={cn(saveSuccess && "bg-emerald-600 text-white hover:bg-emerald-600")}
    >
      {saveSuccess ? (
        <>
          <Check />
          Saved
        </>
      ) : saving ? (
        "Saving..."
      ) : (
        <>
          <Save />
          Save
        </>
      )}
    </Button>
  );
}

// UpdatedByMeta names who last wrote the section, so a surprising setting has
// an author to ask.
export function UpdatedByMeta({
  updatedBy,
  updatedAt,
}: {
  updatedBy?: string;
  updatedAt?: string;
}) {
  if (!updatedBy) return null;
  return (
    <span className="text-xs text-muted-foreground">
      Updated by {updatedBy}
      {updatedAt && ` · ${new Date(updatedAt).toLocaleDateString()}`}
    </span>
  );
}

// UnsavedChangesBanner is the standing reminder that the form differs from
// what the server holds.
export function UnsavedChangesBanner() {
  return <WarningBanner className="py-1.5">You have unsaved changes</WarningBanner>;
}

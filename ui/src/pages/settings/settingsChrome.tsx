import { Save, Check, AlertCircle, RefreshCw, XCircle } from "lucide-react";
import { cn } from "@/lib/utils";

// Chrome shared by the cards on the platform settings page: the banners that
// explain why a section cannot act, the save button, and the last-write meta.
// Every section states those things the same way, so they are written once
// here rather than per card.

export function ErrorBanner({
  message,
  onRetry,
}: {
  message: string;
  onRetry?: () => void;
}) {
  return (
    <div className="flex items-center gap-2 border-b bg-red-50 px-5 py-2.5 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
      <XCircle className="h-3.5 w-3.5 shrink-0" />
      <span className="flex-1">{message}</span>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium hover:bg-red-100 dark:hover:bg-red-900/30"
        >
          <RefreshCw className="h-3 w-3" />
          Retry
        </button>
      )}
    </div>
  );
}

// WarningBanner states a condition that is saved and accepted but leaves the
// section unable to do its job. It never blocks an edit.
export function WarningBanner({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-2 border-b bg-amber-50/50 px-5 py-2 text-xs text-amber-700 dark:bg-amber-950/20 dark:text-amber-400">
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <span>{children}</span>
    </div>
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
}: {
  dirty: boolean;
  saving: boolean;
  saveSuccess: boolean;
  onSave: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onSave}
      disabled={!dirty || saving}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all disabled:opacity-50",
        saveSuccess
          ? "bg-green-600 text-white"
          : "bg-primary text-primary-foreground hover:bg-primary/90",
      )}
    >
      {saveSuccess ? (
        <>
          <Check className="h-3 w-3" />
          Saved
        </>
      ) : saving ? (
        "Saving..."
      ) : (
        <>
          <Save className="h-3 w-3" />
          Save
        </>
      )}
    </button>
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
  return (
    <div className="flex items-center gap-2 border-b bg-amber-50/50 px-5 py-1.5 text-[11px] text-amber-700 dark:bg-amber-950/20 dark:text-amber-400">
      <AlertCircle className="h-3 w-3" />
      You have unsaved changes
    </div>
  );
}

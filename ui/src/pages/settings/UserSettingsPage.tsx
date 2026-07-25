import { useState, useCallback } from "react";
import {
  useNotificationPrefs,
  useSetNotificationPrefs,
  type NotificationMode,
  type NotificationPrefs,
} from "@/api/portal/hooks";
import { ConfigToggle } from "./connections/fields";
import { cn } from "@/lib/utils";
import { Bell, Check, RefreshCw, XCircle } from "lucide-react";

// ---------------------------------------------------------------------------
// Shared error banner
// ---------------------------------------------------------------------------

function ErrorBanner({ message, onRetry }: { message: string; onRetry?: () => void }) {
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

// ---------------------------------------------------------------------------
// Delivery-mode options
// ---------------------------------------------------------------------------

const MODES: { value: NotificationMode; label: string; help: string }[] = [
  { value: "off", label: "Off", help: "No email notifications" },
  { value: "immediate", label: "Immediate", help: "One email per event" },
  { value: "daily", label: "Daily digest", help: "One summary email per day" },
];

// ---------------------------------------------------------------------------
// UserSettingsPage: per-user settings (/settings). Notifications section:
// email delivery mode and category opt-outs (#631). Each change saves
// immediately via a partial PUT (the API accepts only the changed fields),
// with transient inline saved/error feedback.
// ---------------------------------------------------------------------------

export function UserSettingsPage() {
  const {
    data: prefs,
    isLoading,
    error: loadError,
    refetch,
  } = useNotificationPrefs();
  const setPrefs = useSetNotificationPrefs();

  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const save = useCallback(
    (patch: Partial<NotificationPrefs>) => {
      setSaveError(null);
      setSaveSuccess(false);
      setPrefs.mutate(patch, {
        onSuccess: () => {
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2500);
        },
        onError: (err) => {
          setSaveError(err instanceof Error ? err.message : "Failed to save");
        },
      });
    },
    [setPrefs],
  );

  const off = prefs?.mode === "off";

  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      {loadError && (
        <ErrorBanner
          message="Failed to load notification preferences. The server may be unavailable."
          onRetry={() => void refetch()}
        />
      )}

      {/* Header bar */}
      <div className="flex items-center justify-between border-b px-5 py-3">
        <div className="flex items-center gap-3">
          <Bell className="h-4 w-4 text-muted-foreground" />
          <div>
            <h3 className="text-sm font-semibold leading-none">Notifications</h3>
            <p className="mt-1 text-xs text-muted-foreground">
              Email notifications for sharing and feedback activity
            </p>
          </div>
        </div>
        {saveSuccess && (
          <span className="inline-flex items-center gap-1 rounded-md bg-green-600 px-3 py-1.5 text-xs font-medium text-white">
            <Check className="h-3 w-3" />
            Saved
          </span>
        )}
      </div>

      {/* Error banner */}
      {saveError && <ErrorBanner message={saveError} />}

      {isLoading || !prefs ? (
        <div className="p-5 text-sm text-muted-foreground">Loading...</div>
      ) : (
        <div className="space-y-5 p-5">
          {/* Delivery mode: segmented three-way choice */}
          <div>
            <label className="mb-1 block text-xs font-medium">Delivery</label>
            <div role="radiogroup" aria-label="Delivery mode" className="flex flex-wrap gap-2">
              {MODES.map((m) => (
                <button
                  key={m.value}
                  type="button"
                  role="radio"
                  aria-checked={prefs.mode === m.value}
                  onClick={() => save({ mode: m.value })}
                  className={cn(
                    "rounded-md border px-3 py-1.5 text-xs font-medium transition-colors",
                    prefs.mode === m.value
                      ? "border-primary/30 bg-primary/10 text-primary"
                      : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )}
                >
                  {m.label}
                </button>
              ))}
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              {MODES.find((m) => m.value === prefs.mode)?.help}
            </p>
          </div>

          {/* Category toggles: inert while delivery is off */}
          <div
            data-testid="notification-categories"
            className={cn("space-y-4", off && "pointer-events-none opacity-50")}
            aria-disabled={off}
          >
            <ConfigToggle
              label="Shares"
              help="When someone shares an asset, collection, or prompt with you"
              checked={prefs.shares_enabled}
              onChange={(v) => save({ shares_enabled: v })}
            />
            <ConfigToggle
              label="Comments and feedback"
              help="Activity on items you own or that are shared with you"
              checked={prefs.comments_enabled}
              onChange={(v) => save({ comments_enabled: v })}
            />
            <ConfigToggle
              label="Mentions"
              help="When someone names you in a comment with @"
              checked={prefs.mentions_enabled}
              onChange={(v) => save({ mentions_enabled: v })}
            />
          </div>
        </div>
      )}
    </div>
  );
}

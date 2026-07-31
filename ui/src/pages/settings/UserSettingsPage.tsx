import { useState, useCallback } from "react";
import {
  useNotificationPrefs,
  useSetNotificationPrefs,
  type NotificationMode,
  type NotificationPrefs,
  type NotificationPrefsUpdate,
} from "@/api/portal/hooks";
import { useAuthStore } from "@/stores/auth";
import { ConfigToggle } from "./connections/fields";
import { MyNotifications } from "./MyNotifications";
import { cn } from "@/lib/utils";
import { AlertCircle, Bell, Check, RefreshCw, Settings, XCircle } from "lucide-react";

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

interface Props {
  // onNavigate routes an admin to the SMTP settings page from the
  // no-delivery notice. Absent for callers that render the page standalone.
  onNavigate?: (path: string) => void;
}

// ---------------------------------------------------------------------------
// DeliveryModes: the segmented three-way delivery choice. Disabled renders the
// stored choice without offering to change it.
// ---------------------------------------------------------------------------

function DeliveryModes({
  mode,
  disabled,
  onSelect,
}: {
  mode: NotificationMode;
  disabled: boolean;
  onSelect: (mode: NotificationMode) => void;
}) {
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">Delivery</label>
      <div role="radiogroup" aria-label="Delivery mode" className="flex flex-wrap gap-2">
        {MODES.map((m) => (
          <button
            key={m.value}
            type="button"
            role="radio"
            aria-checked={mode === m.value}
            disabled={disabled}
            onClick={() => onSelect(m.value)}
            className={cn(
              "rounded-md border px-3 py-1.5 text-xs font-medium transition-colors",
              mode === m.value
                ? "border-primary/30 bg-primary/10 text-primary"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
              disabled && "cursor-not-allowed opacity-60 hover:bg-transparent",
            )}
          >
            {m.label}
          </button>
        ))}
      </div>
      <p className="mt-1 text-xs text-muted-foreground">
        {MODES.find((m) => m.value === mode)?.help}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// CategoryToggles: the per-category opt-outs. Inert while nothing in them can
// take effect -- delivery off by preference, or no delivery path at all -- but
// still showing the stored values.
// ---------------------------------------------------------------------------

function CategoryToggles({
  prefs,
  disabled,
  onChange,
}: {
  prefs: NotificationPrefs;
  disabled: boolean;
  onChange: (patch: Partial<NotificationPrefsUpdate>) => void;
}) {
  return (
    <div
      data-testid="notification-categories"
      className={cn("space-y-4", disabled && "pointer-events-none opacity-50")}
      aria-disabled={disabled}
    >
      <ConfigToggle
        label="Shares"
        help="When someone shares an asset, collection, or prompt with you"
        checked={prefs.shares_enabled}
        onChange={(v) => onChange({ shares_enabled: v })}
        disabled={disabled}
      />
      <ConfigToggle
        label="Comments and feedback"
        help="Activity on items you own or that are shared with you"
        checked={prefs.comments_enabled}
        onChange={(v) => onChange({ comments_enabled: v })}
        disabled={disabled}
      />
      <ConfigToggle
        label="Mentions"
        help="When someone names you in a comment with @"
        checked={prefs.mentions_enabled}
        onChange={(v) => onChange({ mentions_enabled: v })}
        disabled={disabled}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// NoDeliveryNotice: shown when the platform has no SMTP delivery path. The
// section stays visible with its stored values -- they take effect once an
// admin configures SMTP -- but states plainly that nothing will be sent.
// Admins get the way in; a non-admin has no SMTP endpoint to reach (#1099).
// ---------------------------------------------------------------------------

function NoDeliveryNotice({ onNavigate }: Props) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  return (
    <div
      data-testid="no-delivery-notice"
      className="flex items-start gap-2 border-b bg-amber-50/50 px-5 py-2.5 text-xs text-amber-700 dark:bg-amber-950/20 dark:text-amber-400"
    >
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      <div className="flex-1">
        <p>
          Email delivery is not configured for this platform, so no notification emails
          will be sent. Your preferences are kept and take effect once delivery is
          configured.
        </p>
        {isAdmin && (
          <button
            type="button"
            onClick={() => onNavigate?.("/admin/settings")}
            className="mt-1.5 inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium underline underline-offset-2 hover:bg-amber-100 dark:hover:bg-amber-900/30"
          >
            <Settings className="h-3 w-3" />
            Configure SMTP in Admin &gt; Settings
          </button>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// UserSettingsPage: per-user settings (/settings). It pairs the notification
// preferences (what should I be told) with the notification history (what was
// I actually told), because a user checking one is usually answering the
// other -- "I turned shares on, so why have I had no email?"
// ---------------------------------------------------------------------------

export function UserSettingsPage({ onNavigate }: Props) {
  return (
    <div className="space-y-4">
      <NotificationPrefsCard onNavigate={onNavigate} />
      <MyNotifications />
    </div>
  );
}

// ---------------------------------------------------------------------------
// NotificationPrefsCard: email delivery mode and category opt-outs (#631).
// Each change saves immediately via a partial PUT (the API accepts only the
// changed fields), with transient inline saved/error feedback.
// ---------------------------------------------------------------------------

function NotificationPrefsCard({ onNavigate }: Props) {
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
    (patch: Partial<NotificationPrefsUpdate>) => {
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

  // A false flag is the only signal that delivery is unavailable: an absent
  // field (older server, unwired settings store) must leave the controls live.
  const noDelivery = prefs?.delivery_available === false;
  // Categories are inert while delivery is off by preference, and while the
  // platform cannot deliver at all.
  const off = prefs?.mode === "off" || noDelivery;

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

      {noDelivery && <NoDeliveryNotice onNavigate={onNavigate} />}

      {isLoading || !prefs ? (
        <div className="p-5 text-sm text-muted-foreground">Loading...</div>
      ) : (
        <div className="space-y-5 p-5">
          <DeliveryModes mode={prefs.mode} disabled={noDelivery} onSelect={(mode) => save({ mode })} />

          <CategoryToggles prefs={prefs} disabled={off} onChange={save} />
        </div>
      )}
    </div>
  );
}

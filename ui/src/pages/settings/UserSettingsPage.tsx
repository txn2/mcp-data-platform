import { useState, useCallback } from "react";
import {
  useNotificationPrefs,
  useSetNotificationPrefs,
  type NotificationMode,
  type NotificationPrefs,
  type NotificationPrefsUpdate,
} from "@/api/portal/hooks";
import { useAuthStore } from "@/stores/auth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { ConfigToggle } from "./connections/fields";
import { MyNotifications } from "./MyNotifications";
import { SettingsCard } from "./panels";
import { ErrorBanner, WarningBanner } from "./settingsChrome";
import { cn } from "@/lib/utils";
import { Bell, Check, Settings } from "lucide-react";

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
    <div className="space-y-1.5">
      <Label className="text-xs">Delivery</Label>
      <div role="radiogroup" aria-label="Delivery mode" className="flex flex-wrap gap-2">
        {MODES.map((m) => (
          <Button
            key={m.value}
            type="button"
            role="radio"
            variant="ghost"
            size="sm"
            aria-checked={mode === m.value}
            disabled={disabled}
            onClick={() => onSelect(m.value)}
            className={cn(
              "border",
              mode === m.value
                ? "border-primary/30 bg-primary/10 text-primary hover:bg-primary/10 hover:text-primary"
                : "text-muted-foreground",
            )}
          >
            {m.label}
          </Button>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">
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
    <WarningBanner data-testid="no-delivery-notice">
      <p>
        Email delivery is not configured for this platform, so no notification emails
        will be sent. Your preferences are kept and take effect once delivery is
        configured.
      </p>
      {isAdmin && (
        <Button
          type="button"
          variant="link"
          size="xs"
          onClick={() => onNavigate?.("/admin/settings")}
          className="mt-1 h-auto px-0 text-current underline underline-offset-2"
        >
          <Settings />
          Configure SMTP in Admin &gt; Settings
        </Button>
      )}
    </WarningBanner>
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
    <SettingsCard
      icon={Bell}
      title="Notifications"
      description="Email notifications for sharing and feedback activity"
      notices={
        loadError && (
          <ErrorBanner
            message="Failed to load notification preferences. The server may be unavailable."
            onRetry={() => void refetch()}
          />
        )
      }
      feedback={
        <>
          {saveError && <ErrorBanner message={saveError} />}
          {noDelivery && <NoDeliveryNotice onNavigate={onNavigate} />}
        </>
      }
      action={
        saveSuccess && (
          <Badge variant="success">
            <Check />
            Saved
          </Badge>
        )
      }
    >
      {isLoading || !prefs ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="space-y-5">
          <DeliveryModes
            mode={prefs.mode}
            disabled={noDelivery}
            onSelect={(mode) => save({ mode })}
          />
          <CategoryToggles prefs={prefs} disabled={off} onChange={save} />
        </div>
      )}
    </SettingsCard>
  );
}

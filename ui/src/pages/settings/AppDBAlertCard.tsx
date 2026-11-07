import { useCallback } from "react";
import { Database } from "lucide-react";
import { useAppDBHealthSettings, useSaveAppDBHealthSettings, type AppDBHealthSettings } from "@/api/admin/hooks";
import { Input } from "@/components/ui/input";
import { ChannelsPicker } from "./ChannelsPicker";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners, UpdatedByMeta } from "./settingsChrome";
import { ThresholdsRow } from "./StorageSettingsCard";
import { useSettingsDraft } from "./useSettingsDraft";

// AppDBAlertCard is how every application database is sampled and what
// raises an alert (#2409), in Admin > Settings > Application databases
// (#2528): the sample interval, the history kept, when a statement counts as
// long-running, a warning and a critical threshold for each condition, and the
// channels the alerts post to. Each connection's Health card is judged by the
// same thresholds.

const THRESHOLDS: { key: keyof AppDBHealthSettings; label: string; unit: string }[] = [
  { key: "transaction_id_age", label: "Transaction ID age", unit: "transactions" },
  { key: "idle_in_transaction_seconds", label: "Idle in transaction", unit: "seconds" },
  { key: "connections_percent", label: "Connections", unit: "% of max" },
  { key: "invalid_indexes", label: "Invalid indexes", unit: "indexes" },
  { key: "dead_tuple_percent", label: "Dead rows", unit: "% of a table" },
  { key: "drift_objects", label: "Drift", unit: "objects" },
];

type NumberKey = "interval_seconds" | "history_days" | "long_running_seconds";

export function AppDBAlertCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data } = useAppDBHealthSettings();
  const save = useSaveAppDBHealthSettings();
  const s = useSettingsDraft<AppDBHealthSettings>(data);
  const draft = s.draft;
  const handleSave = useCallback(() => {
    if (!draft) return;
    s.clearError();
    save.mutate(draft, { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);
  if (!draft) return null;
  const num = (key: NumberKey, label: string) => (
    <label className="flex items-center gap-2 text-sm">
      <span className="w-56">{label}</span>
      <Input
        aria-label={label}
        type="number"
        className="h-8 w-28"
        value={draft[key]}
        disabled={isReadOnly}
        onChange={(e) => s.edit({ ...draft, [key]: Number(e.target.value) })}
      />
    </label>
  );
  return (
    <SettingsCard
      icon={Database}
      title="Application databases"
      description="How every application database is sampled and what raises an alert. A threshold of 0 is off."
      feedback={<SaveFeedbackBanners saveError={s.saveError} dirty={s.dirty} />}
    >
      <div className="space-y-3" data-testid="appdb-health-settings">
        {num("interval_seconds", "Sample every (seconds)")}
        {num("history_days", "History days")}
        {num("long_running_seconds", "Long-running after (seconds)")}
        {THRESHOLDS.map((t) => (
          <ThresholdsRow
            key={t.key}
            label={t.label}
            unit={t.unit}
            value={draft[t.key] as AppDBHealthSettings["drift_objects"]}
            disabled={isReadOnly}
            onChange={(v) => s.edit({ ...draft, [t.key]: v })}
          />
        ))}
        <ChannelsPicker
          selected={draft.channels}
          onChange={(channels) => s.edit({ ...draft, channels })}
          help="A condition over a threshold is posted at its level and posted again when it clears."
        />
        <div className="flex items-center justify-between gap-3">
          <UpdatedByMeta updatedBy={draft.updated_by} updatedAt={draft.updated_at} />
          {!isReadOnly && <SaveButton dirty={s.dirty} saving={save.isPending} saveSuccess={s.saveSuccess} onSave={handleSave} />}
        </div>
      </div>
    </SettingsCard>
  );
}

import { useCallback } from "react";
import { HardDrive, Plus, Trash2 } from "lucide-react";
import { useSetStorageSettings, useStorageReport, useStorageSettings } from "@/api/admin/hooks";
import type { StorageSettings, Thresholds } from "@/api/admin/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ChannelsPicker } from "./ChannelsPicker";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners, UpdatedByMeta } from "./settingsChrome";
import { useSettingsDraft } from "./useSettingsDraft";

// StorageSettingsCard is the storage capacity settings (#2148): what each
// store's capacity is where the platform cannot read it, how long the history
// is kept, and the alert thresholds and channels. The SeaweedFS cluster the
// disks are read from is offered only where a bucket's endpoint answers as
// SeaweedFS, or an address is already stored (#2181).

const GIB = 1024 ** 3;

/** gib is a byte count as the GiB the inputs hold, blank for none. */
function gib(bytes: number): string {
  return bytes > 0 ? String(Math.round((bytes / GIB) * 100) / 100) : "";
}

/** bytesOf is a GiB input as bytes, 0 for blank. */
function bytesOf(v: string): number {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? Math.round(n * GIB) : 0;
}

/**
 * useOnSeaweedFS reports whether the SeaweedFS cluster is offered: a bucket's
 * endpoint answers as SeaweedFS, or an address is already stored.
 */
function useOnSeaweedFS(settings: StorageSettings | undefined): boolean {
  const report = useStorageReport();
  return (report.data?.seaweedfs_buckets.length ?? 0) > 0 || !!settings?.seaweedfs.master_url;
}

/** clusterClause names the SeaweedFS cluster in the description where it is offered. */
function clusterClause(onSeaweedFS: boolean): string {
  return onSeaweedFS ? "the SeaweedFS cluster it reads disk space from, " : "";
}

export function StorageSettingsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data, isLoading } = useStorageSettings();
  const save = useSetStorageSettings();
  const s = useSettingsDraft<StorageSettings>(data);
  const draft = s.draft;
  const onSeaweedFS = useOnSeaweedFS(data);

  const handleSave = useCallback(() => {
    if (!draft) return;
    s.clearError();
    const { updated_by: _by, updated_at: _at, ...input } = draft;
    save.mutate(input, { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);

  return (
    <SettingsCard
      icon={HardDrive}
      title="Storage"
      description={`Capacity the platform cannot read for itself, ${clusterClause(onSeaweedFS)}how long each store's history is kept, and when a store filling up is posted to a channel.`}
      feedback={<SaveFeedbackBanners saveError={s.saveError} dirty={s.dirty} />}
      action={
        <>
          <UpdatedByMeta updatedBy={data?.updated_by} updatedAt={data?.updated_at} />
          {!isReadOnly && <SaveButton dirty={s.dirty} saving={save.isPending} saveSuccess={s.saveSuccess} onSave={handleSave} />}
        </>
      }
    >
      {isLoading || !draft ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="space-y-5">
          <Field label="Database limit (GiB)" help="The size the database may reach: its volume, or the plan's quota. Blank is unknown.">
            <Input
              aria-label="Database limit (GiB)"
              inputMode="decimal"
              className="w-40"
              value={gib(draft.database_limit_bytes)}
              disabled={isReadOnly}
              onChange={(e) => s.edit({ ...draft, database_limit_bytes: bytesOf(e.target.value) })}
            />
          </Field>
          <BudgetsEditor draft={draft} onChange={s.edit} disabled={isReadOnly} />
          {onSeaweedFS && <SeaweedFSFields draft={draft} onChange={s.edit} disabled={isReadOnly} />}
          <Field label="History days" help="How long each store's samples are kept for its chart and growth rates.">
            <Input
              aria-label="History days"
              type="number"
              className="w-28"
              value={draft.history_days}
              disabled={isReadOnly}
              onChange={(e) => s.edit({ ...draft, history_days: Number(e.target.value) })}
            />
          </Field>
          <ThresholdsRow
            label="Used %"
            unit="%"
            value={draft.used_percent}
            disabled={isReadOnly}
            onChange={(t) => s.edit({ ...draft, used_percent: t })}
          />
          <ThresholdsRow
            label="Days left"
            unit="days"
            value={draft.days_left}
            disabled={isReadOnly}
            onChange={(t) => s.edit({ ...draft, days_left: t })}
          />
          <ChannelsPicker
            selected={draft.channels}
            onChange={(channels) => s.edit({ ...draft, channels })}
            help="A store over a threshold is posted at its level and repeated per the channel's repeat window; it is posted again when it clears."
          />
        </div>
      )}
    </SettingsCard>
  );
}

/** SeaweedFSFields is the cluster the disks are read from. */
function SeaweedFSFields({
  draft,
  onChange,
  disabled,
}: {
  draft: StorageSettings;
  onChange: (next: StorageSettings) => void;
  disabled: boolean;
}) {
  return (
    <>
      <Field
        label="SeaweedFS master"
        help="The master's address, such as http://seaweedfs-master:9333. Free space is read from the volume servers it lists."
      >
        <Input
          aria-label="SeaweedFS master"
          placeholder="http://seaweedfs-master:9333"
          value={draft.seaweedfs.master_url}
          disabled={disabled}
          onChange={(e) => onChange({ ...draft, seaweedfs: { ...draft.seaweedfs, master_url: e.target.value } })}
        />
      </Field>
      <Field
        label="SeaweedFS volume servers"
        help="Only when the master lists addresses the platform cannot reach: one address per line, read instead of the master's list."
      >
        <textarea
          aria-label="SeaweedFS volume servers"
          className="min-h-16 w-full rounded-md border bg-transparent px-3 py-2 font-mono text-xs"
          value={draft.seaweedfs.volume_urls.join("\n")}
          disabled={disabled}
          onChange={(e) =>
            onChange({ ...draft, seaweedfs: { ...draft.seaweedfs, volume_urls: e.target.value.split("\n") } })
          }
        />
      </Field>
    </>
  );
}

function Field({ label, help, children }: { label: string; help: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-sm">{label}</Label>
      {children}
      <p className="text-xs text-muted-foreground">{help}</p>
    </div>
  );
}

export function ThresholdsRow({
  label,
  unit,
  value,
  disabled,
  onChange,
}: {
  label: string;
  unit: string;
  value: Thresholds;
  disabled: boolean;
  onChange: (t: Thresholds) => void;
}) {
  return (
    <div className="flex flex-wrap items-end gap-3">
      <span className="w-40 text-sm font-medium">{label}</span>
      {(["warning", "critical"] as const).map((level) => (
        <label key={level} className="flex flex-col gap-1 text-xs capitalize text-muted-foreground">
          {level}
          <Input
            aria-label={`${label} ${level}`}
            type="number"
            className="h-8 w-28"
            value={value[level]}
            disabled={disabled}
            onChange={(e) => onChange({ ...value, [level]: Number(e.target.value) })}
          />
        </label>
      ))}
      <span className="pb-2 text-xs text-muted-foreground">{unit}; 0 is off</span>
    </div>
  );
}

function BudgetsEditor({
  draft,
  onChange,
  disabled,
}: {
  draft: StorageSettings;
  onChange: (s: StorageSettings) => void;
  disabled: boolean;
}) {
  const rows = Object.entries(draft.budgets);
  const set = (entries: [string, number][]) => onChange({ ...draft, budgets: Object.fromEntries(entries) });
  return (
    <Field
      label="Bucket budgets (GiB)"
      help="For a bucket on a store whose free space the platform cannot read, such as cloud S3. A budget here overrides MCP_PLATFORM_STORAGE_BUDGETS for its bucket."
    >
      <div className="space-y-2">
        {rows.map(([bucket, bytes], i) => (
          <div key={i} className="flex items-center gap-2">
            <Input
              aria-label="Bucket"
              placeholder="bucket"
              className="w-56"
              value={bucket}
              disabled={disabled}
              onChange={(e) => set(rows.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))}
            />
            <Input
              aria-label="Budget (GiB)"
              inputMode="decimal"
              className="w-32"
              value={gib(bytes)}
              disabled={disabled}
              onChange={(e) => set(rows.map((r, j) => (j === i ? [r[0], bytesOf(e.target.value)] : r)))}
            />
            {!disabled && (
              <Button type="button" variant="ghost" size="icon" aria-label="Remove budget" onClick={() => set(rows.filter((_, j) => j !== i))}>
                <Trash2 />
              </Button>
            )}
          </div>
        ))}
        {!disabled && (
          <Button type="button" variant="outline" size="xs" onClick={() => set([...rows, ["", GIB]])}>
            <Plus />
            Add budget
          </Button>
        )}
      </div>
    </Field>
  );
}

import { useCallback, useState } from "react";
import { Plus, Trash2, Webhook } from "lucide-react";
import { useSetWebhookAlertSettings, useWebhookAlertSettings, useWebhookSources } from "@/api/admin/hooks";
import type { WebhookAlertSettings, WebhookSourceRules, WebhookThresholds } from "@/api/admin/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ChannelsPicker } from "./ChannelsPicker";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners, UpdatedByMeta } from "./settingsChrome";
import { useSettingsDraft } from "./useSettingsDraft";

// WebhookAlertCard is the webhook receiver's alerts (#2149): every condition
// that ends in lost or late data, with a warning and a critical threshold,
// posted to channels and posted again when it clears. The per-receiver rows
// apply to every replica; the per-source rows have defaults and a source's
// own overrides.

type ReplicaKey = "spool_fill_percent" | "spool_age_percent" | "drain_failures" | "spool_volume_free_percent";
type SourceKey = "unauthorized_percent" | "ack_p99_ms" | "compaction_overdue_minutes";

const REPLICA_ROWS: { key: ReplicaKey; label: string; unit: string }[] = [
  { key: "spool_fill_percent", label: "Spool fill", unit: "% of max_bytes" },
  { key: "spool_age_percent", label: "Oldest event waiting", unit: "% of max_age" },
  { key: "drain_failures", label: "Drain failures in a row", unit: "failures" },
  { key: "spool_volume_free_percent", label: "Spool volume free", unit: "% free (lower is worse)" },
];

const SOURCE_ROWS: { key: SourceKey; label: string; unit: string }[] = [
  { key: "unauthorized_percent", label: "Unauthorized share", unit: "% of requests over 15 min" },
  { key: "ack_p99_ms", label: "Acknowledgement p99", unit: "ms over 5 min" },
  { key: "compaction_overdue_minutes", label: "Compaction behind", unit: "min past window end and grace" },
];

const OFF: WebhookThresholds = { warning: 0, critical: 0 };

function NumberCell({ label, value, disabled, onChange }: { label: string; value: number; disabled: boolean; onChange: (n: number) => void }) {
  return (
    <Input
      aria-label={label}
      type="number"
      className="h-8 w-24"
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(Number(e.target.value))}
    />
  );
}

export function WebhookAlertCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data, isLoading } = useWebhookAlertSettings();
  const save = useSetWebhookAlertSettings();
  const s = useSettingsDraft<WebhookAlertSettings>(data);
  const draft = s.draft;

  const handleSave = useCallback(() => {
    if (!draft) return;
    s.clearError();
    const { updated_by: _by, updated_at: _at, ...input } = draft;
    save.mutate(input, { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);

  return (
    <SettingsCard
      icon={Webhook}
      title="Webhooks"
      description="The receiver's conditions that end in lost or late events: posted at warning and at critical, repeated per the channel's repeat window, and posted again when they clear. A post names the source or replica, the value and the threshold, and never carries an event's payload. 0 turns a threshold off."
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
          <ReplicaConditions draft={draft} onChange={s.edit} disabled={isReadOnly} />
          <SourceDefaults draft={draft} onChange={s.edit} disabled={isReadOnly} />
          <SourceOverrides draft={draft} onChange={s.edit} disabled={isReadOnly} />
          <ChannelsPicker
            selected={draft.channels}
            onChange={(channels) => s.edit({ ...draft, channels })}
            help="Each condition is posted at warning and at critical, then repeated per the channel's repeat window until it clears, when it is posted once more."
          />
        </div>
      )}
    </SettingsCard>
  );
}

function ConditionTable({ children }: { children: React.ReactNode }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Condition</TableHead>
          <TableHead>Warning</TableHead>
          <TableHead>Critical</TableHead>
          <TableHead>Measured as</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>{children}</TableBody>
    </Table>
  );
}

function ThresholdRow({
  label,
  unit,
  value,
  disabled,
  onChange,
}: {
  label: string;
  unit: string;
  value: WebhookThresholds;
  disabled: boolean;
  onChange: (t: WebhookThresholds) => void;
}) {
  return (
    <TableRow>
      <TableCell className="font-medium">{label}</TableCell>
      <TableCell>
        <NumberCell label={`${label} warning`} value={value.warning} disabled={disabled} onChange={(n) => onChange({ ...value, warning: n })} />
      </TableCell>
      <TableCell>
        <NumberCell label={`${label} critical`} value={value.critical} disabled={disabled} onChange={(n) => onChange({ ...value, critical: n })} />
      </TableCell>
      <TableCell className="text-xs text-muted-foreground">{unit}</TableCell>
    </TableRow>
  );
}

type Editor = { draft: WebhookAlertSettings; onChange: (d: WebhookAlertSettings) => void; disabled: boolean };

function ReplicaConditions({ draft, onChange, disabled }: Editor) {
  return (
    <div className="space-y-2">
      <h4 className="text-sm font-medium">Each receiver</h4>
      <ConditionTable>
        {REPLICA_ROWS.map((r) => (
          <ThresholdRow
            key={r.key}
            label={r.label}
            unit={r.unit}
            value={draft[r.key]}
            disabled={disabled}
            onChange={(t) => onChange({ ...draft, [r.key]: t })}
          />
        ))}
        <TableRow>
          <TableCell className="font-medium">Drain failing for</TableCell>
          <TableCell className="text-xs text-muted-foreground">—</TableCell>
          <TableCell>
            <NumberCell
              label="Drain failing for critical"
              value={draft.drain_failing_minutes}
              disabled={disabled}
              onChange={(n) => onChange({ ...draft, drain_failing_minutes: n })}
            />
          </TableCell>
          <TableCell className="text-xs text-muted-foreground">minutes, however many failures</TableCell>
        </TableRow>
        <TableRow>
          <TableCell className="font-medium">Receiver holding events not reporting</TableCell>
          <TableCell className="text-xs text-muted-foreground">—</TableCell>
          <TableCell>
            <NumberCell
              label="Not reporting critical"
              value={draft.not_reporting_seconds}
              disabled={disabled}
              onChange={(n) => onChange({ ...draft, not_reporting_seconds: n })}
            />
          </TableCell>
          <TableCell className="text-xs text-muted-foreground">seconds since its last report</TableCell>
        </TableRow>
      </ConditionTable>
    </div>
  );
}

function SourceDefaults({ draft, onChange, disabled }: Editor) {
  const d = draft.defaults;
  const set = (rules: WebhookSourceRules) => onChange({ ...draft, defaults: rules });
  return (
    <div className="space-y-2">
      <h4 className="text-sm font-medium">Each source</h4>
      <ConditionTable>
        {SOURCE_ROWS.map((r) => (
          <ThresholdRow
            key={r.key}
            label={r.label}
            unit={r.unit}
            value={d[r.key] ?? OFF}
            disabled={disabled}
            onChange={(t) => set({ ...d, [r.key]: t })}
          />
        ))}
        <TableRow>
          <TableCell className="font-medium">Refusals</TableCell>
          <TableCell colSpan={2}>
            <NumberCell
              label="Refusal minutes"
              value={d.refusal_minutes ?? 0}
              disabled={disabled}
              onChange={(n) => set({ ...d, refusal_minutes: n })}
            />
          </TableCell>
          <TableCell className="text-xs text-muted-foreground">
            minutes: any spool_full, write_failed or buffer_full is a warning, one every minute is critical
          </TableCell>
        </TableRow>
      </ConditionTable>
    </div>
  );
}

function SourceOverrides({ draft, onChange, disabled }: Editor) {
  const { data } = useWebhookSourcesList();
  const [adding, setAdding] = useState("");
  const names = Object.keys(draft.sources).sort();
  const free = (data ?? []).filter((n) => !draft.sources[n]);
  const setRules = (name: string, rules: WebhookSourceRules | undefined) => {
    const next = { ...draft.sources };
    if (rules) next[name] = rules;
    else delete next[name];
    onChange({ ...draft, sources: next });
  };
  return (
    <div className="space-y-2">
      <h4 className="text-sm font-medium">Source overrides</h4>
      <p className="text-xs text-muted-foreground">
        A source's own thresholds, where it differs from the rest. Silence is judged only for a source given a number
        of minutes here.
      </p>
      {names.map((name) => (
        <SourceOverride
          key={name}
          name={name}
          rules={draft.sources[name] ?? {}}
          disabled={disabled}
          onChange={(r) => setRules(name, r)}
          onRemove={() => setRules(name, undefined)}
        />
      ))}
      {!disabled && free.length > 0 && (
        <div className="flex items-center gap-2">
          <Select value={adding} onValueChange={setAdding}>
            <SelectTrigger size="sm" className="h-8 w-56" aria-label="Source to override">
              <SelectValue placeholder="Choose a source" />
            </SelectTrigger>
            <SelectContent>
              {free.map((n) => (
                <SelectItem key={n} value={n}>
                  {n}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            type="button"
            variant="outline"
            size="xs"
            disabled={!adding}
            onClick={() => {
              setRules(adding, { silent_after_minutes: 60 });
              setAdding("");
            }}
          >
            <Plus />
            Add override
          </Button>
        </div>
      )}
    </div>
  );
}

function SourceOverride({
  name,
  rules,
  disabled,
  onChange,
  onRemove,
}: {
  name: string;
  rules: WebhookSourceRules;
  disabled: boolean;
  onChange: (r: WebhookSourceRules) => void;
  onRemove: () => void;
}) {
  return (
    <div className="rounded-md border p-3" data-testid={`override-${name}`}>
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-medium">{name}</span>
        {!disabled && (
          <Button type="button" variant="ghost" size="icon" aria-label={`Remove ${name} override`} onClick={onRemove}>
            <Trash2 />
          </Button>
        )}
      </div>
      <ConditionTable>
        {SOURCE_ROWS.map((r) => (
          <ThresholdRow
            key={r.key}
            label={`${name} ${r.label}`}
            unit={rules[r.key] ? r.unit : `${r.unit} (default while 0/0)`}
            value={rules[r.key] ?? OFF}
            disabled={disabled}
            onChange={(t) => onChange({ ...rules, [r.key]: t })}
          />
        ))}
        <TableRow>
          <TableCell className="font-medium">Silent after</TableCell>
          <TableCell colSpan={2}>
            <NumberCell
              label={`${name} silent after minutes`}
              value={rules.silent_after_minutes ?? 0}
              disabled={disabled}
              onChange={(n) => onChange({ ...rules, silent_after_minutes: n })}
            />
          </TableCell>
          <TableCell className="text-xs text-muted-foreground">minutes with no event, a warning; 0 is off</TableCell>
        </TableRow>
      </ConditionTable>
    </div>
  );
}

/** useWebhookSourcesList is the names of the webhook sources an override can name. */
function useWebhookSourcesList() {
  const q = useWebhookSources();
  return { data: q.data?.sources.map((s) => s.name) };
}

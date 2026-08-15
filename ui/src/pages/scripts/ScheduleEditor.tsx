import { useState } from "react";
import {
  useSetScriptSchedule,
  useSetScriptScheduleEnabled,
} from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatWhen } from "./runFormat";

// ScheduleEditor is where the person accountable for an automation says when it
// runs. A cadence carries no authority — the execution gate and the capability
// grant are read again at every fire — so it belongs to the script's owner
// rather than to an administrator, and it is the one thing this surface writes.

// COMMON_CADENCES are the cadences most reports actually want, offered so the
// common case is a click rather than a remembered cron expression. The field
// stays free text, because the platform accepts any standard expression.
const COMMON_CADENCES: { label: string; cron: string }[] = [
  { label: "Every weekday, 07:00", cron: "0 7 * * 1-5" },
  { label: "Daily, 07:00", cron: "0 7 * * *" },
  { label: "Weekly, Monday 07:00", cron: "0 7 * * 1" },
  { label: "Hourly", cron: "0 * * * *" },
];

export function ScheduleEditor({ contract }: { contract: ScriptContract }) {
  const schedule = contract.schedule;
  const save = useSetScriptSchedule(contract.id);
  const setEnabled = useSetScriptScheduleEnabled(contract.id);
  const busy = save.isPending || setEnabled.isPending;
  const failure = save.error ?? setEnabled.error;

  return (
    <SectionCard
      title="Schedule"
      action={
        schedule ? (
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => setEnabled.mutate(!schedule.enabled)}
          >
            {schedule.enabled ? "Pause" : "Resume"}
          </Button>
        ) : null
      }
    >
      <div className="space-y-3">
        <ScheduleState contract={contract} />

        {failure ? (
          <Alert variant="destructive">
            <AlertDescription>{failure.message}</AlertDescription>
          </Alert>
        ) : null}

        <CadenceForm
          schedule={schedule}
          busy={busy}
          onSave={(cron, timezone) => save.mutate({ cron, timezone })}
        />

        <p className="text-xs text-muted-foreground">
          The cadence is read in the timezone above, so a report keeps its wall-clock time
          across a daylight-saving change. Every fire runs the approved version under the
          capabilities its approval bound.
        </p>
      </div>
    </SectionCard>
  );
}

// CadenceForm is the input half: when it runs, in which zone, and the common
// cadences offered as a click. It holds the draft the reader is typing, so the
// section above it stays about what the schedule is doing.
function CadenceForm({
  schedule,
  busy,
  onSave,
}: {
  schedule: ScriptContract["schedule"];
  busy: boolean;
  onSave: (cron: string, timezone: string) => void;
}) {
  const [cron, setCron] = useState(schedule?.cron_spec ?? "");
  const [timezone, setTimezone] = useState(
    schedule?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
  );
  const incomplete = cron.trim() === "" || timezone.trim() === "";

  return (
    <>
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-56 flex-1 space-y-1.5">
          <Label htmlFor="script-cron">Cadence</Label>
          <Input
            id="script-cron"
            value={cron}
            placeholder="0 7 * * 1-5"
            className="font-mono"
            onChange={(e) => setCron(e.target.value)}
          />
        </div>
        <div className="min-w-48 flex-1 space-y-1.5">
          <Label htmlFor="script-timezone">Timezone</Label>
          <Input
            id="script-timezone"
            value={timezone}
            placeholder="America/Los_Angeles"
            className="font-mono"
            onChange={(e) => setTimezone(e.target.value)}
          />
        </div>
        <Button
          disabled={busy || incomplete}
          onClick={() => onSave(cron.trim(), timezone.trim())}
        >
          {schedule ? "Update schedule" : "Schedule it"}
        </Button>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {COMMON_CADENCES.map((c) => (
          <Button key={c.cron} size="sm" variant="outline" onClick={() => setCron(c.cron)}>
            {c.label}
          </Button>
        ))}
      </div>
    </>
  );
}

// ScheduleState says what the cadence is doing now: when it next fires, that it
// is paused, or that nothing will execute it however it is timed.
function ScheduleState({ contract }: { contract: ScriptContract }) {
  const schedule = contract.schedule;
  if (!contract.approval.approved) {
    return (
      <Alert>
        <AlertDescription>
          A cadence set here is kept, and will start firing as soon as a version of this
          script is approved. Until then nothing executes it.
        </AlertDescription>
      </Alert>
    );
  }
  if (!schedule) {
    return (
      <p className="text-sm text-muted-foreground">
        This script runs on demand. Give it a cadence and the platform runs it for you.
      </p>
    );
  }
  return (
    <p className="flex flex-wrap items-center gap-2 text-sm">
      <Badge variant={schedule.enabled ? "success" : "muted"}>
        {schedule.enabled ? "Scheduled" : "Paused"}
      </Badge>
      <span className="font-mono text-xs">
        {schedule.cron_spec} ({schedule.timezone})
      </span>
      <span className="text-xs text-muted-foreground">
        {schedule.enabled ? `next ${formatWhen(schedule.next_run_at)}` : "no next fire"}
      </span>
    </p>
  );
}

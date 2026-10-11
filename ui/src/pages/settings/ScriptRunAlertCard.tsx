import { useState, useEffect, useCallback } from "react";
import { CalendarX } from "lucide-react";
import { useScriptRunAlert, useSetScriptRunAlert } from "@/api/admin/hooks";
import { ChannelsPicker } from "./ChannelsPicker";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners, UpdatedByMeta } from "./settingsChrome";

// The scheduled-run failure alert (#1724). A failed scheduled run already
// emails its script's owner, who can fix it; this card names the channels an
// operations team watches, so a nightly automation failing is seen there too,
// for every script at once.
export function ScriptRunAlertCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data: settings, isLoading } = useScriptRunAlert();
  const save = useSetScriptRunAlert();

  const [channels, setChannels] = useState<string[]>([]);
  const [dirty, setDirty] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (!settings) return;
    setChannels(settings.channels ?? []);
    setDirty(false);
  }, [settings]);

  const handleChange = useCallback((next: string[]) => {
    setChannels(next);
    setDirty(true);
    setSaveSuccess(false);
    setSaveError(null);
  }, []);

  const handleSave = useCallback(() => {
    setSaveError(null);
    save.mutate(
      { channels },
      {
        onSuccess: () => {
          setDirty(false);
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2500);
        },
        onError: (err) => setSaveError(err instanceof Error ? err.message : "Failed to save"),
      },
    );
  }, [channels, save]);

  return (
    <SettingsCard
      icon={CalendarX}
      title="Scheduled run failures"
      description="When a scheduled run of any automation fails, its owner is emailed. Choose channels here to post the failure there too, with a link to the run."
      feedback={<SaveFeedbackBanners saveError={saveError} dirty={dirty} />}
      action={
        <>
          <UpdatedByMeta updatedBy={settings?.updated_by} updatedAt={settings?.updated_at} />
          {!isReadOnly && (
            <SaveButton dirty={dirty} saving={save.isPending} saveSuccess={saveSuccess} onSave={handleSave} />
          )}
        </>
      }
    >
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <ChannelsPicker
          selected={channels}
          onChange={handleChange}
          help="An automation that keeps failing posts once per the channel's repeat window, and the next post says how many failures were held back."
        />
      )}
    </SettingsCard>
  );
}

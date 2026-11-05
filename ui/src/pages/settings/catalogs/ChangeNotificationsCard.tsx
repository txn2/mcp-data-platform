import { useCallback, useEffect, useState } from "react";
import { BellRing } from "lucide-react";

import { useAPIChangeSettings, useSaveAPIChangeSettings } from "@/api/admin/hooks";
import { useUnsavedChangesGuard } from "@/lib/navigationGuard";
import { ChannelsPicker } from "../ChannelsPicker";
import { SettingsCard } from "../panels";
import { SaveButton, SaveFeedbackBanners } from "../settingsChrome";

// Change notifications (#2483, #2486): the channels an API spec's change
// report and an upstream's warning about an operation are posted to. One
// setting for every catalog and connection, as the scheduled-run alert is one
// for every automation.
export function ChangeNotificationsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data: settings, isLoading } = useAPIChangeSettings();
  const save = useSaveAPIChangeSettings();
  const [channels, setChannels] = useState<string[]>([]);
  const [dirty, setDirty] = useState(false);
  useUnsavedChangesGuard(dirty);
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
      icon={BellRing}
      title="Change notifications"
      description="Where a spec's change report is posted when a check finds its upstream different, and where an upstream's Deprecation, Sunset or removal of an operation is posted the first time it is seen."
      feedback={<SaveFeedbackBanners saveError={saveError} dirty={dirty} />}
      action={
        !isReadOnly && (
          <SaveButton dirty={dirty} saving={save.isPending} saveSuccess={saveSuccess} onSave={handleSave} />
        )
      }
    >
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <ChannelsPicker
          selected={channels}
          onChange={handleChange}
          help="Each report names the operations that changed and the scripts, endpoints and prompts that use them."
        />
      )}
    </SettingsCard>
  );
}

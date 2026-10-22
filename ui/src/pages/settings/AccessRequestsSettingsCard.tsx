import { useCallback, useId } from "react";
import { KeyRound } from "lucide-react";
import {
  useAccessRequestSettings,
  useSetAccessRequestSettings,
} from "@/api/admin/hooks";
import type { AccessRequestSettings } from "@/api/admin/types";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners } from "./settingsChrome";
import { useSettingsDraft } from "./useSettingsDraft";

// AccessRequestsSettingsCard is what a person who cannot open an item is
// shown, and how long their request for access waits for an answer (#2090).

export function AccessRequestsSettingsCard({
  isReadOnly,
}: {
  isReadOnly: boolean;
}) {
  const ids = useId();
  const { data, isLoading } = useAccessRequestSettings();
  const save = useSetAccessRequestSettings();
  const s = useSettingsDraft<AccessRequestSettings>(data);
  const draft = s.draft;

  const handleSave = useCallback(() => {
    if (!draft) return;
    s.clearError();
    save.mutate(draft, { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);

  return (
    <SettingsCard
      icon={KeyRound}
      title="Access requests"
      description="A signed-in person who follows a link to an item they cannot open can ask its owner for access."
      feedback={<SaveFeedbackBanners saveError={s.saveError} dirty={s.dirty} />}
      action={
        !isReadOnly && (
          <SaveButton
            dirty={s.dirty}
            saving={save.isPending}
            saveSuccess={s.saveSuccess}
            onSave={handleSave}
          />
        )
      }
    >
      {isLoading || !draft ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="max-w-md space-y-4">
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={draft.reveal_names}
              disabled={isReadOnly}
              onChange={(e) =>
                s.edit({ ...draft, reveal_names: e.target.checked })
              }
            />
            <span>
              Show the item's name on the request page
              <span className="block text-xs text-muted-foreground">
                Off, the page says only that the person does not have access,
                and names the owner.
              </span>
            </span>
          </label>
          <div className="grid grid-cols-[minmax(0,1fr)_8rem] items-center gap-x-4">
            <div>
              <Label htmlFor={`${ids}-expire`}>Requests expire after</Label>
              <p className="text-xs text-muted-foreground">
                An unanswered request can be made again once it expires.
              </p>
            </div>
            <div className="flex items-center gap-2">
              <Input
                id={`${ids}-expire`}
                type="number"
                min={1}
                max={365}
                className="h-8"
                disabled={isReadOnly}
                value={draft.expire_days}
                onChange={(e) =>
                  s.edit({ ...draft, expire_days: Number(e.target.value) })
                }
              />
              <span className="text-xs text-muted-foreground">days</span>
            </div>
          </div>
        </div>
      )}
    </SettingsCard>
  );
}

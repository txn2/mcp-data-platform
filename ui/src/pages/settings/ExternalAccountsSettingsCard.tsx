import { useCallback, useId } from "react";
import { UserRound } from "lucide-react";
import { useExternalAccountSettings, useSetExternalAccountSettings } from "@/api/admin/hooks";
import type { ExternalAccountSettings } from "@/api/admin/types";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners } from "./settingsChrome";
import { useSettingsDraft } from "./useSettingsDraft";

// ExternalAccountsSettingsCard is how long an external user's session and
// sign-in links last (#2092): the session, the invitation email's link, and a
// link asked for on the sign-in screen.

const FIELDS: { key: keyof ExternalAccountSettings; label: string; unit: string; max: number; help: string }[] = [
  { key: "session_hours", label: "Session", unit: "hours", max: 720, help: "How long a signed-in external user stays signed in." },
  { key: "invite_hours", label: "Invitation link", unit: "hours", max: 720, help: "How long the link in the invitation email works." },
  { key: "link_minutes", label: "Sign-in link", unit: "minutes", max: 1440, help: "How long a link asked for on the sign-in screen works. Each link works once." },
];

export function ExternalAccountsSettingsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const ids = useId();
  const { data, isLoading } = useExternalAccountSettings();
  const save = useSetExternalAccountSettings();
  const s = useSettingsDraft<ExternalAccountSettings>(data);
  const draft = s.draft;

  const handleSave = useCallback(() => {
    if (!draft) return;
    s.clearError();
    save.mutate(draft, { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);

  return (
    <SettingsCard
      icon={UserRound}
      title="External accounts"
      description="People outside the organization sign in with a one-time link emailed to them. These set how long a session and each link last."
      feedback={<SaveFeedbackBanners saveError={s.saveError} dirty={s.dirty} />}
      action={!isReadOnly && <SaveButton dirty={s.dirty} saving={save.isPending} saveSuccess={s.saveSuccess} onSave={handleSave} />}
    >
      {isLoading || !draft ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="grid max-w-md grid-cols-[minmax(0,1fr)_8rem] items-center gap-x-4 gap-y-4">
          {FIELDS.map((f) => (
            <div key={f.key} className="contents">
              <div>
                <Label htmlFor={`${ids}-${f.key}`}>{f.label}</Label>
                <p className="text-xs text-muted-foreground">{f.help}</p>
              </div>
              <div className="flex items-center gap-2">
                <Input
                  id={`${ids}-${f.key}`}
                  type="number"
                  min={1}
                  max={f.max}
                  className="h-8"
                  disabled={isReadOnly}
                  value={draft[f.key]}
                  onChange={(e) => s.edit({ ...draft, [f.key]: Number(e.target.value) })}
                />
                <span className="text-xs text-muted-foreground">{f.unit}</span>
              </div>
            </div>
          ))}
        </div>
      )}
    </SettingsCard>
  );
}

import { useCallback, useId, useMemo } from "react";
import { Film } from "lucide-react";
import { useEmbedHostsSettings, useSetEmbedHosts } from "@/api/admin/hooks";
import { Label } from "@/components/ui/label";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners } from "./settingsChrome";
import { useSettingsDraft } from "./useSettingsDraft";

// EmbedHostsSettingsCard is the list of https hosts an HTML asset may frame and
// play media from, in the portal and on share links (#2069). One host per
// line; the server normalizes and refuses anything else, naming the entry.

/** The lines of the text area, blank lines dropped. */
export function hostLines(text: string): string[] {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");
}

export function EmbedHostsSettingsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const ids = useId();
  const { data, isLoading } = useEmbedHostsSettings();
  const save = useSetEmbedHosts();
  const server = useMemo(() => (data ? data.hosts.join("\n") : undefined), [data]);
  const s = useSettingsDraft<string>(server);
  const draft = s.draft;

  const handleSave = useCallback(() => {
    if (draft === undefined) return;
    s.clearError();
    save.mutate(hostLines(draft), { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);

  return (
    <SettingsCard
      icon={Film}
      title="Embed hosts"
      description="Video players and media hosts an HTML asset or slide deck may embed, in the portal and on share links."
      feedback={<SaveFeedbackBanners saveError={s.saveError} dirty={s.dirty} />}
      action={
        !isReadOnly && (
          <SaveButton dirty={s.dirty} saving={save.isPending} saveSuccess={s.saveSuccess} onSave={handleSave} />
        )
      }
    >
      {isLoading || draft === undefined || !data ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="max-w-xl space-y-3">
          <div className="space-y-1">
            <Label htmlFor={`${ids}-hosts`}>Hosts, one per line</Label>
            <textarea
              id={`${ids}-hosts`}
              className="min-h-40 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm"
              spellCheck={false}
              disabled={isReadOnly}
              value={draft}
              onChange={(e) => s.edit(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              An https host such as player.example.com, with a port or a leading *. if it needs one. An empty list
              turns embeds off. YouTube and Vimeo videos show as a link that opens the video in a new tab.
            </p>
          </div>
          {!isReadOnly && (
            <button
              type="button"
              className="text-sm text-primary underline-offset-2 hover:underline"
              onClick={() => s.edit(data.configured.join("\n"))}
            >
              Restore the configured list
            </button>
          )}
        </div>
      )}
    </SettingsCard>
  );
}

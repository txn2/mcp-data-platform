import { useNotificationChannels } from "@/api/admin/hooks";
import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";

// ChannelsPicker chooses the notification channels an alert is also posted to
// (#1724), from the channels configured in the Notification Channels card.
// It is one component for every alert card, as RecipientsEditor is, so the
// three cannot drift on what choosing a channel means.
//
// A selected name that no longer matches a configured channel is still shown,
// marked missing, so an operator sees the stale entry and can remove it; the
// server refuses to store a name it does not know.
export function ChannelsPicker({
  selected,
  onChange,
  help,
}: {
  selected: string[];
  onChange: (next: string[]) => void;
  help: string;
}) {
  const { data, isLoading } = useNotificationChannels();
  const configured = data?.channels ?? [];
  const known = new Set(configured.map((c) => c.name));
  const missing = selected.filter((name) => !known.has(name));

  function toggle(name: string) {
    onChange(selected.includes(name) ? selected.filter((n) => n !== name) : [...selected, name]);
  }

  return (
    <div className="space-y-1.5" data-testid="channels-picker">
      <Label className="text-xs">Channels</Label>
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading channels…</p>
      ) : configured.length === 0 && missing.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No notification channels are configured. Add one in the Notification Channels section to
          post this alert to it.
        </p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {configured.map((ch) => (
            <label key={ch.name} className="flex cursor-pointer items-center gap-1.5 text-sm">
              <input
                type="checkbox"
                checked={selected.includes(ch.name)}
                onChange={() => toggle(ch.name)}
                aria-label={`Post to ${ch.name}`}
              />
              {ch.name}
              {!ch.enabled && <Badge variant="secondary">disabled</Badge>}
            </label>
          ))}
          {missing.map((name) => (
            <label key={name} className="flex cursor-pointer items-center gap-1.5 text-sm text-destructive">
              <input type="checkbox" checked onChange={() => toggle(name)} aria-label={`Post to ${name}`} />
              {name}
              <Badge variant="destructive">missing</Badge>
            </label>
          ))}
        </div>
      )}
      <p className="text-xs text-muted-foreground">{help}</p>
    </div>
  );
}

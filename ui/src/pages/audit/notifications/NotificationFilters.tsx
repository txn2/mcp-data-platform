import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Input } from "@/components/ui/input";
import { STAT_TILES } from "./StatusTiles";

const CATEGORIES = [
  { value: "share", label: "Shares" },
  { value: "comment", label: "Comments" },
  { value: "mention", label: "Mentions" },
  { value: "channel", label: "Channels" },
];

// NotificationFilters narrows the delivery history to one recipient, one
// delivery state, one kind of notification, or one channel. Extracted from
// NotificationsTab.tsx (#1207).
export function NotificationFilters({
  recipient,
  status,
  category,
  channel,
  channels,
  onRecipient,
  onStatus,
  onCategory,
  onChannel,
}: {
  recipient: string;
  status: string;
  category: string;
  channel: string;
  // channels names the configured channels the channel filter offers.
  channels: string[];
  onRecipient: (v: string) => void;
  onStatus: (v: string) => void;
  onCategory: (v: string) => void;
  onChannel: (v: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-2">
      <Input
        type="text"
        value={recipient}
        onChange={(e) => onRecipient(e.target.value)}
        placeholder="Filter by recipient"
        aria-label="Filter by recipient"
        className="h-8 w-56"
      />
      <FilterSelect
        label="Filter by status"
        value={status}
        onChange={onStatus}
        options={[
          { value: "", label: "All statuses" },
          ...STAT_TILES.map((t) => ({ value: t.key, label: t.label })),
        ]}
      />
      <FilterSelect
        label="Filter by category"
        value={category}
        onChange={onCategory}
        options={[{ value: "", label: "All categories" }, ...CATEGORIES]}
      />
      {channels.length > 0 && (
        <FilterSelect
          label="Filter by channel"
          value={channel}
          onChange={onChannel}
          options={[
            { value: "", label: "All channels" },
            ...channels.map((name) => ({ value: name, label: name })),
          ]}
        />
      )}
    </div>
  );
}

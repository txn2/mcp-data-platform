import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Input } from "@/components/ui/input";
import { STAT_TILES } from "./StatusTiles";

const CATEGORIES = [
  { value: "share", label: "Shares" },
  { value: "comment", label: "Comments" },
  { value: "mention", label: "Mentions" },
];

// NotificationFilters narrows the delivery history to one recipient, one
// delivery state, or one kind of notification. Extracted from
// NotificationsTab.tsx (#1207).
export function NotificationFilters({
  recipient,
  status,
  category,
  onRecipient,
  onStatus,
  onCategory,
}: {
  recipient: string;
  status: string;
  category: string;
  onRecipient: (v: string) => void;
  onStatus: (v: string) => void;
  onCategory: (v: string) => void;
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
    </div>
  );
}

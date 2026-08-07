import { Button } from "@/components/ui/button";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";

// TIME_RANGE_PRESETS is the window every dashboard view offers. The MCP and
// API Gateway views share one store, so switching the range on one carries to
// the other; they therefore have to offer the same set.
const TIME_RANGE_PRESETS: { value: TimeRangePreset; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];

// TimeRangePicker is the dashboard's window chooser, reading and writing the
// shared time-range store directly so a view only has to place it.
export function TimeRangePicker() {
  const { preset, setPreset } = useTimeRangeStore();
  return (
    <div className="flex items-center gap-1">
      {TIME_RANGE_PRESETS.map((p) => (
        <Button
          key={p.value}
          type="button"
          size="xs"
          variant={preset === p.value ? "default" : "ghost"}
          onClick={() => setPreset(p.value)}
          className={preset === p.value ? undefined : "text-muted-foreground"}
        >
          {p.label}
        </Button>
      ))}
    </div>
  );
}

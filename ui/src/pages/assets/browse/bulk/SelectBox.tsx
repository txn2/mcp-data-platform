import { cn } from "@/lib/utils";

/**
 * SelectBox is one row's box in a bulk selection (#2507). A click with shift
 * held reports it, so the list can select the range from the last box ticked.
 * It sits beside the row's own link, never inside it, so ticking a box never
 * opens the asset.
 */
export function SelectBox({
  label,
  checked,
  indeterminate,
  onToggle,
  className,
}: {
  label: string;
  checked: boolean;
  indeterminate?: boolean;
  onToggle: (shift: boolean) => void;
  className?: string;
}) {
  return (
    <label className={cn("flex cursor-pointer items-center justify-center", className)}>
      <input
        type="checkbox"
        aria-label={label}
        checked={checked}
        ref={(el) => {
          if (el) el.indeterminate = !!indeterminate && !checked;
        }}
        className="size-5 cursor-pointer accent-primary sm:size-4"
        onClick={(e) => {
          e.stopPropagation();
          onToggle(e.shiftKey);
        }}
        // The change is applied by onClick, which knows whether shift was held.
        onChange={() => {}}
      />
    </label>
  );
}

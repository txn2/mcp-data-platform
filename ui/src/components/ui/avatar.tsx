import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * Avatar is a round mark for a person, a group or a mode: initials or an icon
 * on a tint. The tint is the caller's, chosen so the pair reads at 4.5:1 in
 * light and dark (see components/share/model.ts TINTS).
 */
export function Avatar({
  children,
  tint,
  size = 36,
  className,
  label,
}: {
  children: ReactNode;
  tint: string;
  size?: 22 | 36;
  className?: string;
  /** What the mark stands for, when nothing beside it says. */
  label?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center justify-center rounded-full font-semibold select-none",
        size === 36 ? "size-9 text-[13px] [&_svg]:size-[18px]" : "size-[22px] text-[10px] [&_svg]:size-3",
        tint,
        className,
      )}
      aria-hidden={label ? undefined : true}
      aria-label={label}
      role={label ? "img" : undefined}
    >
      {children}
    </span>
  );
}

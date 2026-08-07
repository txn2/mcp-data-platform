import { Search } from "lucide-react";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

// SearchInput is the one shape a list's free-text filter takes: a ui/input with
// the magnifier inside its leading edge, so a filter bar reads as "search here"
// before any placeholder is read. The icon is decorative; the placeholder (or an
// explicit aria-label) names the field.
//
// `className` sizes the control within its bar (the wrapper is what flexes);
// input-level classes go on `inputClassName`.
export function SearchInput({
  className,
  inputClassName,
  ...props
}: React.ComponentProps<typeof Input> & { inputClassName?: string }) {
  return (
    <div className={cn("relative", className)}>
      <Search
        aria-hidden
        className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
      />
      <Input type="text" className={cn("pl-9", inputClassName)} {...props} />
    </div>
  );
}

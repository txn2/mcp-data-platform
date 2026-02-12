import { cn } from "@/lib/utils";

type Variant = "success" | "error" | "warning" | "neutral";

interface StatusBadgeProps {
  variant: Variant;
  children: React.ReactNode;
}

const variantStyles: Record<Variant, string> = {
  success: "bg-green-100 text-green-800",
  error: "bg-red-100 text-red-800",
  warning: "bg-yellow-100 text-yellow-800",
  neutral: "bg-gray-100 text-gray-800",
};

export function StatusBadge({ variant, children }: StatusBadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium",
        variantStyles[variant],
      )}
    >
      {children}
    </span>
  );
}

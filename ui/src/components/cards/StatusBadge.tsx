import { Badge } from "@/components/ui/badge";

type Variant = "success" | "error" | "warning" | "neutral";

interface StatusBadgeProps {
  variant: Variant;
  children: React.ReactNode;
}

const variantMap: Record<Variant, "success" | "danger" | "warning" | "muted"> = {
  success: "success",
  error: "danger",
  warning: "warning",
  neutral: "muted",
};

export function StatusBadge({ variant, children }: StatusBadgeProps) {
  return <Badge variant={variantMap[variant]}>{children}</Badge>;
}

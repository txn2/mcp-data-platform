import { CheckCircle2 } from "lucide-react";
import { useSignoff } from "@/api/portal/hooks";
import { Badge } from "@/components/ui/badge";

// SignoffBadge renders "signed off by N of M stakeholders" for an asset or
// collection (#603). Hidden until the summary loads.
export function SignoffBadge({
  targetType,
  id,
  enabled = true,
}: {
  targetType: "assets" | "collections";
  id: string;
  enabled?: boolean;
}) {
  const { data } = useSignoff(targetType, id, enabled);
  if (!data) return null;
  const complete = data.signed_off >= data.stakeholders;
  return (
    <Badge
      variant={complete ? "success" : "muted"}
      className="font-normal"
      title="Distinct stakeholders who approved out of owner + active share grantees"
    >
      <CheckCircle2 /> Signed off by {data.signed_off} of {data.stakeholders}
    </Badge>
  );
}

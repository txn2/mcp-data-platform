import { Badge } from "@/components/ui/badge";

/**
 * BuiltinBadge marks a knowledge page the platform ships and reconciles at
 * startup (#1390): read-only where people edit, hidable per deployment. One
 * component so the card, the detail header, and any future list render the
 * same marker with the same explanation.
 */
export function BuiltinBadge() {
  return (
    <Badge variant="info" title="Shipped with the platform and updated by release; read-only.">
      Built-in
    </Badge>
  );
}

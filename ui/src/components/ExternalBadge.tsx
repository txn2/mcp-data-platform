import { Badge } from "@/components/ui/badge";

/**
 * ExternalBadge marks a person or a persona outside the organization (#2290):
 * an account whose role resolves to a persona marked external. It is shown
 * wherever people are chosen or listed, so whoever shares can tell.
 */
export function ExternalBadge() {
  return <Badge variant="info">External</Badge>;
}

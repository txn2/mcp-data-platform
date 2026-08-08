import { usePractitionerWorklist, useSMEWorklist } from "@/api/portal/hooks";
import { useInsightStats } from "@/api/admin/hooks";

/** What a nav item says about work waiting in its section. */
export interface NavBadge {
  count: number;
  /** Names the count where only a dot fits, e.g. on a collapsed rail. */
  label: string;
}

/**
 * useNavBadges counts the work waiting on the reader in the two sections that
 * accumulate any, keyed by nav path.
 *
 * The portal sends no push notifications, so the rail is where someone finds
 * out that a thread needs their answer or an insight needs their review
 * without opening the page (#617).
 */
export function useNavBadges(isAdmin: boolean): Record<string, NavBadge> {
  const practitionerWorklist = usePractitionerWorklist();
  const smeWorklist = useSMEWorklist();
  const feedback =
    (practitionerWorklist.data?.total ?? 0) + (smeWorklist.data?.total ?? 0);

  // The team-wide pending count is admin-scoped, so the fetch is gated on
  // isAdmin to avoid a 401 poll for a non-admin reviewer (see #662).
  const insightStats = useInsightStats({ enabled: isAdmin });
  const knowledge = isAdmin ? (insightStats.data?.total_pending ?? 0) : 0;

  return {
    "/feedback": {
      count: feedback,
      label: `${feedback} feedback items need you`,
    },
    "/knowledge": {
      count: knowledge,
      label: `${knowledge} insights awaiting review`,
    },
  };
}

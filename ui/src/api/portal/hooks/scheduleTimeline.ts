import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// The Schedules tab's data (#1891): when every schedule the caller may see
// fires, laid out by the server on three axes. Every fire is the scheduler's
// own, expanded server-side with the parse that fires the schedule; nothing
// here computes one.

/** ScheduleSection is which axis a schedule is drawn on. */
export type ScheduleSection = "intraday" | "multi_day" | "long_term";

/** ScheduleRhythm is the typical gap between a schedule's fires. */
export type ScheduleRhythm = "minutes" | "hours" | "days" | "weeks" | "months";

/** ScheduleFireRow is one schedule's fires within its section's window. */
export interface ScheduleFireRow {
  script_id: string;
  script_name: string;
  // The script's owner, category and tags (#1992), which the tab's filters
  // narrow on; tags is [] for none.
  owner_email: string;
  category: string;
  tags: string[];
  cron_spec: string;
  timezone: string;
  enabled: boolean;
  rhythm: ScheduleRhythm;
  // fire_count is every fire in the window; fires holds at most the server's
  // per-row cap of them, and truncated says when it holds fewer.
  fire_count: number;
  truncated: boolean;
  fires: string[];
  // last_fire is the window's last fire, set on a truncated row (#1933): the
  // list stops at the cap, the schedule does not.
  last_fire?: string;
}

/** ScheduleFireWindow is one axis and the rows drawn on it. */
export interface ScheduleFireWindow {
  section: ScheduleSection;
  from: string;
  to: string;
  rows: ScheduleFireRow[];
}

/** ScheduleUnreadable is a schedule the server could not expand. */
export interface ScheduleUnreadable {
  script_id: string;
  script_name: string;
  reason: string;
}

/** ScheduleTimeline is every visible schedule laid out for one viewer. */
export interface ScheduleTimeline {
  timezone: string;
  sections: ScheduleFireWindow[];
  unreadable: ScheduleUnreadable[];
}

/** viewerTimezone is the zone the browser draws in, which is the zone the
 * server is asked to cut the windows in so the two agree on midnight. */
export function viewerTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

// useScheduleTimeline reads the fire layout for the viewer's own zone.
// Visibility is the run listing's: the caller's own schedules, and every
// schedule for an administrator.
export function useScheduleTimeline() {
  const tz = viewerTimezone();
  return useQuery({
    queryKey: [...scriptsKey, "fires", tz],
    queryFn: () =>
      apiFetch<ScheduleTimeline>(`/scripts/fires?tz=${encodeURIComponent(tz)}`),
  });
}

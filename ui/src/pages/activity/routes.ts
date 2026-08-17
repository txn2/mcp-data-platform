// The Activity section's route shape, in one module.
//
// A session detail is addressable so a reader can bookmark it or hand it to a
// colleague, and it is reached from two directions: the sessions list, and the
// asset viewer of anything a session saved. Both build the path from here, so
// the link an asset carries and the route the shell matches cannot drift apart.

/** The Activity dashboard: the reader's aggregates. */
export const ACTIVITY_ROUTE = "/activity";

/** The reader's own sessions, listed. */
export const MY_SESSIONS_ROUTE = "/activity/sessions";

/** Where one of the reader's own sessions opens. */
export function mySessionPath(sessionId: string): string {
  return `${MY_SESSIONS_ROUTE}/${encodeURIComponent(sessionId)}`;
}

/** The reader's own recorded calls, listed. */
export const MY_CALLS_ROUTE = "/activity/calls";

/** Where one of the reader's own calls opens. */
export function myCallPath(callId: string): string {
  return `${MY_CALLS_ROUTE}/${encodeURIComponent(callId)}`;
}

/** The call id in a detail route, or null when route is not one. */
export function myCallIdFrom(route: string): string | null {
  const match = route.match(/^\/activity\/calls\/(.+)$/);
  return match ? decodeURIComponent(match[1]!) : null;
}

/** The session id in a detail route, or null when route is not one. */
export function mySessionIdFrom(route: string): string | null {
  const match = route.match(/^\/activity\/sessions\/(.+)$/);
  return match ? decodeURIComponent(match[1]!) : null;
}

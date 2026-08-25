// The share link a reader arrived on, read back off the portal address.
//
// A share opened by a signed-in platform user does not render the public page:
// the server sends them to the same object in their own portal, which is where
// the version history, the feedback threads and the editing are (#1473). The
// token that got them there rides along in the query string, because the one
// thing the portal page cannot show is what the recipient of that link sees —
// and the person who sent it is the most common reader of it.

/** SHARE_TOKEN_PARAM is the query parameter the redirect carries the token in.
 * It must match `shareTokenParam` in pkg/portal/public.go. */
export const SHARE_TOKEN_PARAM = "share";

/** SHARE_PUBLIC_PARAM asks the share route for the public page itself rather
 * than for the object behind it. It must match `publicPageParam` in
 * pkg/portal/public.go. */
export const SHARE_PUBLIC_PARAM = "public";

// A token is hex: the server generates it with hex.EncodeToString over random
// bytes (internal/portal/portaldomain/sharebuild.go). Anything else came from
// somewhere other than a share link, and is not turned into a path — a link
// built from an arbitrary query value is a link to wherever that value points.
const TOKEN_SHAPE = /^[0-9a-f]{16,128}$/;

/**
 * shareTokenFromSearch returns the share token in a location search string, or
 * null when there is none or it is not the shape the server issues.
 */
export function shareTokenFromSearch(search: string): string | null {
  const token = new URLSearchParams(search).get(SHARE_TOKEN_PARAM);
  if (!token || !TOKEN_SHAPE.test(token)) return null;
  return token;
}

/**
 * sharedPagePath is the public viewer address for a token.
 *
 * It asks for the public page explicitly (`public=1`, read by
 * `publicPageRequested` in pkg/portal/public.go). Without that the server would
 * send the reader who clicked it back to the portal page they clicked it from,
 * because they are exactly the caller the redirect is for.
 */
export function sharedPagePath(token: string): string {
  return `/portal/view/${token}?${SHARE_PUBLIC_PARAM}=1`;
}

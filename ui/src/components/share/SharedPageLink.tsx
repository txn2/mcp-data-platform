import { ExternalLink } from "lucide-react";
import { Button } from "@/components/ui/button";
import { sharedPagePath, shareTokenFromSearch } from "@/lib/shareLink";

/**
 * The way back to the shared page, for a reader who got here by opening a share
 * link (#1473).
 *
 * A signed-in platform user who opens one is sent to the object in their own
 * portal rather than to the public page, and the token they arrived on is in
 * the address. This offers it back, because the one thing this page cannot show
 * is what the recipient of that link sees — and the person who sent the link is
 * the most common reader of it.
 *
 * Renders nothing when the address carries no token, which is every other way
 * of reaching the page.
 */
export function SharedPageLink() {
  const token = shareTokenFromSearch(
    typeof window === "undefined" ? "" : window.location.search,
  );
  if (!token) return null;
  return (
    <Button asChild variant="outline" size="sm">
      <a
        href={sharedPagePath(token)}
        target="_blank"
        rel="noopener noreferrer"
        title="Open the shared page as its recipient sees it"
      >
        <ExternalLink />
        Shared page
      </a>
    </Button>
  );
}

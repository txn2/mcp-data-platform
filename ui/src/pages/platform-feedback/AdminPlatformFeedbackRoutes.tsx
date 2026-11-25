import { PlatformFeedbackDetailPage } from "./PlatformFeedbackDetailPage";
import { PlatformFeedbackPage } from "./PlatformFeedbackPage";

// AdminPlatformFeedbackRoutes is the Platform feedback section (#2820): the
// list and one post.

const LIST_ROUTE = "/admin/platform-feedback";
const POST_ROUTE = /^\/admin\/platform-feedback\/([^/]+)$/;

export function AdminPlatformFeedbackRoutes({
  route,
  onNavigate,
  onBack,
}: {
  route: string;
  onNavigate: (path: string) => void;
  onBack: (fallback: string) => void;
}) {
  if (route === LIST_ROUTE) return <PlatformFeedbackPage onNavigate={onNavigate} />;
  const post = route.match(POST_ROUTE);
  if (!post) return null;
  return <PlatformFeedbackDetailPage id={decodeURIComponent(post[1]!)} onBack={() => onBack(LIST_ROUTE)} />;
}

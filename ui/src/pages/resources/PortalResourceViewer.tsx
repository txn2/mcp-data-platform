import { mySessionPath } from "@/pages/activity/routes";
import { folderAddress } from "./parts/libraryUrl";
import { ResourceViewerPage } from "./ResourceViewerPage";

// PortalResourceViewer is one managed resource on the portal's own surface. The
// libraries, sessions and scripts it links out to are the reader's own, which
// is the whole of what separates it from the console's view of the same file.
export function PortalResourceViewer({
  resourceId,
  navigate,
  goBack,
}: {
  resourceId: string;
  navigate: (path: string) => void;
  goBack: (fallback: string) => void;
}) {
  return (
    <ResourceViewerPage
      resourceId={resourceId}
      onBack={() => goBack("/resources")}
      onOpenFolder={(tab, path) => navigate(folderAddress("/resources", tab, path))}
      onNavigate={navigate}
      // A script that wrote this file opens on the reader's own scripts
      // section, which shows it to its owner and to an administrator and
      // answers everybody else not-found -- the same rule its page applies.
      scriptPath={(id) => `/scripts/${encodeURIComponent(id)}`}
      sessionPath={mySessionPath}
    />
  );
}

import { Sidebar } from "./Sidebar";

/** The sidebar as a mobile overlay: a dimmed backdrop that closes it, and the
    sidebar itself pinned over the page. */
export function MobileSidebarOverlay({
  currentPath,
  onNavigate,
  onClose,
}: {
  currentPath: string;
  onNavigate: (path: string) => void;
  onClose: () => void;
}) {
  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/50" onClick={onClose} />
      <div className="fixed inset-y-0 left-0 z-50">
        <Sidebar
          currentPath={currentPath}
          onNavigate={onNavigate}
          collapsed={false}
          onToggleCollapse={() => {}}
          mobile
          onClose={onClose}
        />
      </div>
    </>
  );
}

import { useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { KnowledgePageList } from "./parts/KnowledgePageList";
import { KnowledgePageDetail } from "./parts/KnowledgePageDetail";
import { KnowledgePageForm } from "./parts/KnowledgePageForm";

// Re-exported so existing importers (e.g. KnowledgePageForm.test.tsx) can keep
// importing the form from this module.
export { KnowledgePageForm };

type Mode = { view: "list" } | { view: "page"; id: string } | { view: "edit"; id: string } | { view: "create" };

/**
 * KnowledgePagesPage is the portal home for canonical business/domain knowledge
 * pages. Everyone can browse and read; create/edit/remove is shown only to
 * personas with apply_knowledge access (admins). It reuses the shared
 * MarkdownEditor and MarkdownRenderer.
 *
 * The page detail view is URL-addressable (#709): the open page is driven by the
 * /knowledge/pages/:id route via the openPageId prop, so detail is deep-linkable,
 * shareable, and supports browser back/forward. The hub keys this subtree by
 * path, so opening another page remounts with a fresh openPageId. Create and edit
 * are transient sub-states layered over the current route.
 */
export function KnowledgePagesPage({
  openPageId,
  onNavigate,
}: {
  // The knowledge page to open in detail, from the /knowledge/pages/:id route.
  // Undefined renders the page list (the /knowledge/pages route).
  openPageId?: string;
  // Navigate to an in-app path (page detail routing and entity-reference chips).
  onNavigate?: (path: string) => void;
} = {}) {
  const [mode, setMode] = useState<Mode>(
    openPageId ? { view: "page", id: openPageId } : { view: "list" },
  );

  // Open/leave page detail through real navigation when a navigator is present
  // (the hub always provides one) so the URL stays the source of truth; fall back
  // to in-component state when rendered standalone without a navigator.
  const openDetail = (id: string) =>
    onNavigate ? onNavigate(`/knowledge/pages/${id}`) : setMode({ view: "page", id });
  const backToList = () =>
    onNavigate ? onNavigate("/knowledge/pages") : setMode({ view: "list" });

  // Wiki-style back (#709): from page B reached by clicking through page A, "Back"
  // returns to A. AppShell records the path each navigation came from in
  // history.state.from, so we only step back through real browser history when the
  // previous entry was itself a knowledge page. Reaching this detail from anywhere
  // else (an asset viewer, a search result, a feedback surface, or a cold
  // deep-link) returns to the page list instead of ejecting out of Knowledge.
  const goBack = () => {
    const from = typeof window !== "undefined" ? window.history.state?.from : undefined;
    if (onNavigate && typeof from === "string" && from.startsWith("/knowledge/pages")) {
      window.history.back();
    } else {
      backToList();
    }
  };

  // Create/edit/remove gates on the apply_knowledge capability (a tool-access
  // gate, not an admin-role gate), or admin. This mirrors the REST handler's
  // userHasToolAccess (pkg/portal/knowledge_page_handler.go): the capability
  // grants non-admins, and admins are allowed too since apply_knowledge may be
  // unregistered on a deployment (#661).
  const canEdit = useAuthStore(
    (s) => (s.user?.tools?.includes("apply_knowledge") ?? false) || s.isAdmin(),
  );

  if (mode.view === "create") {
    return (
      <KnowledgePageForm
        key="create"
        // Cancel (onDone with no id) returns to the list in-component: the create
        // form never changed the URL (it is still /knowledge/pages), so navigating
        // there would be a no-op remount and leave the form on screen (#709).
        onDone={(id) => (id ? openDetail(id) : setMode({ view: "list" }))}
      />
    );
  }
  if (mode.view === "edit") {
    // key by id so switching edit targets always remounts with fresh hydration.
    return <KnowledgePageForm key={mode.id} id={mode.id} onDone={(id) => setMode({ view: "page", id: id ?? mode.id })} />;
  }
  if (mode.view === "page") {
    return (
      <KnowledgePageDetail
        id={mode.id}
        canEdit={canEdit}
        onNavigate={onNavigate}
        onBack={goBack}
        onEdit={() => setMode({ view: "edit", id: mode.id })}
        onDeleted={backToList}
      />
    );
  }
  return (
    <KnowledgePageList
      canEdit={canEdit}
      onOpen={openDetail}
      onCreate={() => setMode({ view: "create" })}
      onNavigate={onNavigate}
    />
  );
}

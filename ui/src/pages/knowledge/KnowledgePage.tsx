// KnowledgePage hosts the knowledge-review surfaces. The tab bodies and their
// detail drawers were decomposed into src/pages/knowledge/capture/ under the
// 600-line max-lines backstop (#819, following the #766 pattern). They are
// re-exported here so existing importers (KnowledgeHub, KnowledgePage.test.tsx)
// keep resolving the same public surface from this module.
export { KnowledgeCaptureTab } from "./capture/KnowledgeCaptureTab";
export { ChangesetsTab } from "./capture/ChangesetsTab";

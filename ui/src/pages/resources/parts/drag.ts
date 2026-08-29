/**
 * What a drag inside the resource library carries.
 *
 * Two kinds, distinguished by the data-transfer type rather than by parsing a
 * payload, so a drop target reads what it was handed before it decides whether
 * it can take it. The types are lowercase because the DataTransfer API
 * lowercases every format it is given, and a reader comparing against a
 * capitalized constant would never match.
 */
export const DRAG_RESOURCE = "application/x-mcp-resources";
export const DRAG_FOLDER = "application/x-mcp-folder";

export type Dragged =
  | { kind: "resources"; ids: string[] }
  | { kind: "folder"; path: string }
  | { kind: "none" };

/** readDrag reports what a drop is carrying. */
export function readDrag(data: DataTransfer): Dragged {
  const ids = data.getData(DRAG_RESOURCE);
  if (ids) return { kind: "resources", ids: ids.split(",").filter(Boolean) };
  const path = data.getData(DRAG_FOLDER);
  if (path) return { kind: "folder", path };
  return { kind: "none" };
}

/**
 * dragResources marks a drag as carrying files.
 *
 * Dragging a row that is part of a selection carries the whole selection, and
 * dragging one that is not carries just it -- the behavior every file manager
 * has, and the one that stops a drag from silently discarding a selection the
 * person just made.
 */
export function dragResources(data: DataTransfer, id: string, selected: string[]) {
  const ids = selected.includes(id) ? selected : [id];
  data.setData(DRAG_RESOURCE, ids.join(","));
  data.effectAllowed = "move";
}

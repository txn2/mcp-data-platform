export interface SectionDraft {
  id: string;
  title: string;
  description: string;
  items: ItemDraft[];
}

export interface ItemDraft {
  id: string;
  asset_id: string;
  assetName?: string;
  assetContentType?: string;
}

let draftIdCounter = 0;
export function draftId() {
  return `draft-${++draftIdCounter}`;
}

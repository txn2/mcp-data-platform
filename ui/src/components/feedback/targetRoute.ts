import { FileText, FolderOpen, Sparkles, Megaphone } from "lucide-react";
import type { ThreadTargetType } from "@/api/portal/types";

// targetRoute maps a thread's target to a portal route, an icon, and a type
// label, so the activity feed and the slide-over can link back to the asset,
// collection, or prompt a thread lives on. Standalone threads have no per-object
// destination, so their route is null and no "Go to item" link is rendered.

export interface TargetMeta {
  route: string | null;
  label: string;
  Icon: typeof FileText;
}

export interface TargetRef {
  target_type: ThreadTargetType;
  asset_id?: string;
  collection_id?: string;
  prompt_id?: string;
}

export function targetMeta(t: TargetRef): TargetMeta {
  switch (t.target_type) {
    case "asset":
      return { route: t.asset_id ? `/assets/${t.asset_id}` : null, label: "Asset", Icon: FileText };
    case "collection":
      return {
        route: t.collection_id ? `/collections/${t.collection_id}` : null,
        label: "Collection",
        Icon: FolderOpen,
      };
    case "prompt":
      return { route: t.prompt_id ? `/prompts/${t.prompt_id}` : null, label: "Prompt", Icon: Sparkles };
    default:
      return { route: null, label: "Channel", Icon: Megaphone };
  }
}

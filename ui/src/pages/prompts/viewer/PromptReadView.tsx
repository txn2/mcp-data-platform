import type { Dispatch, SetStateAction } from "react";
import { Eye, Code } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import { ArgumentsPanel } from "./ArgumentsPanel";
import type { ViewMode } from "./types";

// PromptReadView is the non-editing detail view: metadata strip, arguments
// summary, the preview/source toggle, and the rendered content body. Shown when
// the viewer is not in edit mode. Extracted verbatim from PromptViewerPage.tsx
// (#819).
export function PromptReadView({
  prompt,
  viewMode,
  setViewMode,
}: {
  prompt: Prompt;
  viewMode: ViewMode;
  setViewMode: Dispatch<SetStateAction<ViewMode>>;
}) {
  return (
    <>
      {/* Metadata strip */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-3 rounded-lg border bg-card p-3 text-xs">
        <div><span className="text-muted-foreground">Name:</span> <span className="font-mono select-all">{prompt.name}</span></div>
        <div><span className="text-muted-foreground">Category:</span> <span>{prompt.category || "—"}</span></div>
        <div className="md:col-span-2"><span className="text-muted-foreground">Description:</span> <span className="break-words">{markdownToPlainText(prompt.description) || "—"}</span></div>
        <div><span className="text-muted-foreground">Owner:</span> <span>{prompt.owner_email || "—"}</span></div>
        <div><span className="text-muted-foreground">Updated:</span> <span>{prompt.updated_at ? new Date(prompt.updated_at).toLocaleString() : "—"}</span></div>
        {prompt.tags && prompt.tags.length > 0 && (
          <div className="md:col-span-2 flex items-center gap-1.5 flex-wrap">
            <span className="text-muted-foreground">Tags:</span>
            {prompt.tags.map((t) => (
              <span key={t} className="inline-flex items-center rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
                {t}
              </span>
            ))}
          </div>
        )}
      </div>

      {/* Arguments */}
      <ArgumentsPanel args={prompt.arguments} />

      {/* View mode toggle */}
      <div className="flex items-center gap-2">
        <div className="inline-flex rounded-md border text-sm">
          <button
            type="button"
            onClick={() => setViewMode("preview")}
            className={cn("flex items-center gap-1.5 px-3 py-1.5 rounded-l-md", viewMode === "preview" ? "bg-accent font-medium" : "hover:bg-accent/50")}
          >
            <Eye className="h-3.5 w-3.5" /> Preview
          </button>
          <button
            type="button"
            onClick={() => setViewMode("source")}
            className={cn("flex items-center gap-1.5 px-3 py-1.5 rounded-r-md border-l", viewMode === "source" ? "bg-accent font-medium" : "hover:bg-accent/50")}
          >
            <Code className="h-3.5 w-3.5" /> Source
          </button>
        </div>
      </div>

      {/* Content body */}
      {viewMode === "preview" ? (
        <div className="rounded-lg border bg-card p-6">
          <MarkdownRenderer content={prompt.content} />
        </div>
      ) : (
        <pre className="rounded-lg border bg-card p-6 text-sm overflow-auto whitespace-pre-wrap font-mono">{prompt.content}</pre>
      )}
    </>
  );
}

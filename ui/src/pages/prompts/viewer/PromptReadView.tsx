import type { Dispatch, SetStateAction } from "react";
import { Eye, Code } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { markdownToPlainText } from "@/lib/markdownText";
import { ArgumentsPanel } from "./ArgumentsPanel";
import type { ViewMode } from "./types";

// PromptReadView is the non-editing detail view: metadata, arguments summary,
// the preview/source toggle, and the rendered content body. Shown when the
// viewer is not in edit mode.
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
      <SectionCard title="Details">
        <dl className="grid grid-cols-1 gap-x-6 gap-y-1.5 text-xs md:grid-cols-2">
          <Detail label="Name">
            <span className="font-mono select-all">{prompt.name}</span>
          </Detail>
          <Detail label="Category">{prompt.category || "—"}</Detail>
          <Detail label="Description" className="md:col-span-2">
            <span className="break-words">{markdownToPlainText(prompt.description) || "—"}</span>
          </Detail>
          <Detail label="Owner">{prompt.owner_email || "—"}</Detail>
          <Detail label="Updated">
            {prompt.updated_at ? new Date(prompt.updated_at).toLocaleString() : "—"}
          </Detail>
          {prompt.tags && prompt.tags.length > 0 && (
            <Detail label="Tags" className="md:col-span-2">
              <span className="inline-flex flex-wrap items-center gap-1.5">
                {prompt.tags.map((t) => (
                  <Badge key={t} variant="muted" className="text-[11px]">
                    {t}
                  </Badge>
                ))}
              </span>
            </Detail>
          )}
        </dl>
      </SectionCard>

      <ArgumentsPanel args={prompt.arguments} />

      {/* The content, as it renders and as it is stored. */}
      <Tabs value={viewMode} onValueChange={(v) => setViewMode(v as ViewMode)}>
        <TabsList>
          <TabsTrigger value="preview">
            <Eye /> Preview
          </TabsTrigger>
          <TabsTrigger value="source">
            <Code /> Source
          </TabsTrigger>
        </TabsList>
      </Tabs>

      <Card>
        <CardContent>
          {viewMode === "preview" ? (
            <MarkdownRenderer content={prompt.content} />
          ) : (
            <pre className="overflow-auto font-mono text-sm whitespace-pre-wrap">
              {prompt.content}
            </pre>
          )}
        </CardContent>
      </Card>
    </>
  );
}

function Detail({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={className}>
      <dt className="inline text-muted-foreground">{label}:</dt>{" "}
      <dd className="inline">{children}</dd>
    </div>
  );
}

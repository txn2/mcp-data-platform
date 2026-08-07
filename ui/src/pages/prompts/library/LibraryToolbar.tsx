import { FolderOpen, Plus, Search } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { Tab } from "./types";

// LibraryToolbar is the prompt library's one control strip: the bucket the
// reader is in, the search that spans both buckets, and the two things they can
// start from here. The buckets are tabs rather than buttons because switching
// them replaces the whole list below, which is what a tab means.
export function LibraryToolbar({
  tab,
  onTabChange,
  mineCount,
  libraryCount,
  search,
  onSearchChange,
  onManageCollections,
  onCreate,
}: {
  tab: Tab;
  onTabChange: (tab: Tab) => void;
  mineCount: number;
  libraryCount: number;
  search: string;
  onSearchChange: (value: string) => void;
  onManageCollections: () => void;
  // Absent on the Library tab: a reader cannot author into the shared bucket.
  onCreate?: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Tabs value={tab} onValueChange={(v) => onTabChange(v as Tab)}>
        <TabsList>
          <TabsTrigger value="mine">
            My Prompts
            <Badge variant="muted" className="text-[11px]">
              {mineCount}
            </Badge>
          </TabsTrigger>
          <TabsTrigger value="library">
            Library
            <Badge variant="muted" className="text-[11px]">
              {libraryCount}
            </Badge>
          </TabsTrigger>
        </TabsList>
      </Tabs>

      <div className="relative max-w-md flex-1">
        <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          placeholder="Search prompts by meaning..."
          className="pl-9"
        />
      </div>

      <Button variant="outline" onClick={onManageCollections}>
        <FolderOpen /> Collections
      </Button>
      {onCreate && (
        <Button className="ml-auto" onClick={onCreate}>
          <Plus /> New Prompt
        </Button>
      )}
    </div>
  );
}

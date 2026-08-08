import {
  FileUp,
  Globe,
  Users,
  User,
  FolderOpen,
  type LucideIcon,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { usePersonas } from "@/api/admin/hooks";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CATEGORIES } from "./modals/shared";
import { ResourceModals, useResourceModals } from "./parts/ResourceModals";
import { ResourceResults } from "./parts/ResourceResults";
import { useResourceLibrary, type ResourceSort } from "./parts/useResourceLibrary";

interface Props {
  admin?: boolean;
  onNavigate?: (path: string) => void;
}

const CATEGORY_OPTIONS = [
  { value: "", label: "All categories" },
  ...CATEGORIES.map((c) => ({ value: c, label: c })),
];

const SORT_OPTIONS = [
  { value: "updated", label: "Recently updated" },
  { value: "last_read", label: "Recently read" },
];

// scopeTabs is the set of libraries a caller can look at. A reader sees their
// own, their persona's, and the global one; an admin sees every persona's plus
// the unfiltered "All".
function scopeTabs(
  admin: boolean,
  personaNames: string[],
  userPersona: string | undefined,
): { key: string; label: string; icon: LucideIcon }[] {
  if (!admin) {
    return [
      { key: "user", label: "My Resources", icon: User },
      ...(userPersona ? [{ key: userPersona, label: userPersona, icon: Users }] : []),
      { key: "global", label: "Global", icon: Globe },
    ];
  }
  return [
    { key: "all", label: "All Resources", icon: FolderOpen },
    { key: "global", label: "Global", icon: Globe },
    ...personaNames.map((name) => ({ key: name, label: name, icon: Users })),
    { key: "user", label: "User", icon: User },
  ];
}

export function ResourcesPage({ admin = false }: Props) {
  const userPersona = useAuthStore((s) => s.user?.persona);
  const { data: personaData } = usePersonas(admin);
  const personaNames = (personaData?.personas ?? []).map((p) => p.name);

  const tabs = scopeTabs(admin, personaNames, userPersona);
  const library = useResourceLibrary(
    admin,
    tabs.map((t) => t.key),
  );
  const modals = useResourceModals();

  return (
    <Tabs value={library.activeTab} onValueChange={library.setActiveTab} className="gap-4">
      {/* An admin sees a tab per persona, so this bar is the one that has to
          survive a long list: it scrolls rather than wrapping, and its faces are
          tight enough that a typical deployment's personas still fit. */}
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-0 overflow-x-auto border-b p-0"
      >
        {tabs.map((tab) => (
          <TabsTrigger
            key={tab.key}
            value={tab.key}
            className="flex-none gap-1 px-2.5 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
          >
            <tab.icon className="size-3.5" />
            {tab.label}
          </TabsTrigger>
        ))}
      </TabsList>

      {/* One library body redrawn per scope: only the active tab's panel is
          mounted, so the filter bar and list below are written once. */}
      {tabs.map((tab) => (
        <TabsContent key={tab.key} value={tab.key} className="space-y-4">
          <div className="flex flex-wrap items-center gap-3">
            <SearchInput
              className="min-w-[200px] flex-1"
              value={library.searchInput}
              onChange={(e) => library.setSearchInput(e.target.value)}
              placeholder="Search resources..."
              aria-label="Search resources"
            />
            <FilterSelect
              label="Filter by category"
              value={library.category}
              onChange={library.setCategory}
              options={CATEGORY_OPTIONS}
              className="h-9 text-sm"
            />
            {admin && (
              <FilterSelect
                label="Sort resources"
                value={library.sort}
                onChange={(v) => library.setSort(v as ResourceSort)}
                options={SORT_OPTIONS}
                className="h-9 text-sm"
              />
            )}
            <Button onClick={() => modals.setUploading(true)}>
              <FileUp />
              Upload
            </Button>
          </div>

          <ResourceResults
            resources={library.resources}
            isLoading={library.isLoading}
            filtering={library.filtering}
            admin={admin}
            onOpen={modals.setDetail}
            onUpload={() => modals.setUploading(true)}
          />

          <InfiniteFooter
            hasMore={library.hasNextPage}
            isLoadingMore={library.isFetchingNextPage}
            onLoadMore={library.fetchNextPage}
          />

          {library.total > library.resources.length && (
            <p className="text-center text-sm text-muted-foreground">
              Showing {library.resources.length} of {library.total} resources
            </p>
          )}
        </TabsContent>
      ))}

      <ResourceModals state={modals} admin={admin} personaNames={personaNames} />
    </Tabs>
  );
}

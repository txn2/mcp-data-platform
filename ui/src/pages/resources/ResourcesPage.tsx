import { useState } from "react";
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
import { CATEGORIES } from "./shared";
import { UploadModal } from "./modals/UploadModal";
import { ResourceResults } from "./parts/ResourceResults";
import { useResourceLibrary, type ResourceSort } from "./parts/useResourceLibrary";
import { adminReachNote, canWriteScope, libraryCopy, targetForTab } from "./scopes";

interface Props {
  admin?: boolean;
  onNavigate?: (path: string, opts?: { replace?: boolean }) => void;
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

export function ResourcesPage({ admin = false, onNavigate }: Props) {
  const user = useAuthStore((s) => s.user);
  const userPersona = user?.persona;
  const { data: personaData } = usePersonas(admin);
  const personaNames = (personaData?.personas ?? []).map((p) => p.name);

  // The section this library belongs to, which is both where its own address
  // is written and where a resource opened from it lives.
  const basePath = admin ? "/admin/resources" : "/resources";
  // Which surface is asking. On the reader's own portal the platform-admin
  // override does not apply, so Upload is offered on the libraries the caller
  // themselves may add to and nowhere else; the note in its place says where
  // an administrator exercises the rest of their authority.
  const surface = admin ? "admin" : "portal";
  const reachNote = adminReachNote(user, surface);
  const tabs = scopeTabs(admin, personaNames, userPersona);
  const library = useResourceLibrary(
    admin,
    tabs.map((t) => t.key),
    { basePath, onNavigate },
  );
  const [uploading, setUploading] = useState(false);

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
      {tabs.map((tab) => {
        // The Upload control is offered only on a tab the caller may actually
        // add to, read from the same authority the server applies to the
        // request. Where it is not offered, the note in its place says who
        // fills this library instead.
        const target = targetForTab(tab.key, user);
        const writable = canWriteScope(user, target, surface);
        const source = [libraryCopy(target).source, reachNote].filter(Boolean).join(" ");
        return (
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
              {writable ? (
                <Button onClick={() => setUploading(true)}>
                  <FileUp />
                  Upload
                </Button>
              ) : (
                <p data-testid="scope-read-only" className="text-xs text-muted-foreground">
                  {source}
                </p>
              )}
            </div>

            <ResourceResults
              resources={library.resources}
              isLoading={library.isLoading}
              filtering={library.filtering}
              admin={admin}
              readOnlyNote={writable ? undefined : source}
              onOpen={(r) => {
                // The entry being left has to carry the view, or Back returns
                // to a plain library rather than to this one.
                onNavigate?.(library.address, { replace: true });
                onNavigate?.(`${basePath}/${r.id}`);
              }}
              onUpload={() => setUploading(true)}
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
        );
      })}

      {uploading && (
        <UploadModal
          onClose={() => setUploading(false)}
          admin={admin}
          personaNames={personaNames}
          destination={targetForTab(library.activeTab, user)}
        />
      )}
    </Tabs>
  );
}

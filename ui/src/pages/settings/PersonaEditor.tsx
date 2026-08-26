import { AlertCircle, Info } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { PersonaDraft } from "./persona/types";
import { EditorHeader } from "./persona/EditorHeader";
import { IdentityRulesAside } from "./persona/IdentityRulesAside";
import { BehaviorTab } from "./persona/BehaviorTab";
import { PermissionsExplorer } from "./persona/PermissionsExplorer";
import { usePersonaEditor } from "./persona/usePersonaEditor";

// Public re-exports keep the import surface stable for existing consumers:
// PersonasPanel imports { PersonaEditor, type PersonaDraft } and the
// PersonaEditor.resolve.test.ts suite imports { resolve } from this module.
export { resolve } from "./persona/resolve";
export type { PersonaDraft } from "./persona/types";

// PersonaEditor is the master/detail editor for a single persona: identity +
// allow/deny rules on the left, and a tabbed right area (live Permissions
// preview / AI Assistant Behavior). The pure resolution engine, the editor's
// state (usePersonaEditor), and the three facets (identity+rules, permissions
// explorer, behavior) live under ./persona/ (#766, #1206) so this file owns
// only the layout composition.

interface PersonaEditorProps {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  onSave: () => void;
  onCancel: () => void;
  isCreate: boolean;
  dirty: boolean;
  selectedName: string | null;
  canDelete?: boolean;
  onDelete?: () => void;
  sourceNote?: string | null;
  isReadOnly?: boolean;
}

export function PersonaEditor({
  draft,
  onUpdate,
  onSave,
  onCancel,
  isCreate,
  dirty,
  selectedName,
  canDelete = false,
  onDelete,
  sourceNote = null,
  isReadOnly = false,
}: PersonaEditorProps) {
  const editor = usePersonaEditor({
    draft,
    onUpdate,
    onSave,
    isCreate,
    selectedName,
  });

  return (
    <div className="flex h-full flex-col">
      <EditorHeader
        title={isCreate ? "New Persona" : draft.displayName || selectedName}
        isCreate={isCreate}
        isReadOnly={isReadOnly}
        dirty={dirty}
        canDelete={canDelete}
        onDelete={onDelete}
        onCancel={onCancel}
        onSave={editor.handleSave}
        saveDisabled={
          isReadOnly ||
          editor.isPending ||
          (!dirty && !isCreate) ||
          !draft.name ||
          !draft.displayName
        }
        saving={editor.isPending}
        saveSuccess={editor.saveSuccess}
      />

      {sourceNote && (
        <Alert className="rounded-none border-x-0 border-t-0 bg-muted/30 px-6 py-2">
          <Info />
          <AlertDescription className="text-xs">{sourceNote}</AlertDescription>
        </Alert>
      )}

      {editor.saveError && (
        <Alert
          variant="destructive"
          className="rounded-none border-x-0 border-t-0 px-6 py-2"
        >
          <AlertCircle />
          <AlertDescription className="text-xs">{editor.saveError}</AlertDescription>
        </Alert>
      )}

      {/* ─── MAIN: left identity/rules + tabbed right area ─── */}
      {/*
        Stack vertically below lg (1024px) so the scope tabs in the center
        column remain reachable on narrow viewports — without this, the
        fixed 300px left rail starves the right column of usable width and
        users can't switch the Allow/Deny editor between tools and
        connections scope.
      */}
      <div className="flex min-h-0 flex-1 flex-col lg:grid lg:grid-cols-[300px_minmax(0,1fr)]">
        <IdentityRulesAside
          isReadOnly={isReadOnly}
          isCreate={isCreate}
          draft={draft}
          onUpdate={onUpdate}
          scope={editor.scope}
          allowList={editor.allowList}
          denyList={editor.denyList}
          items={editor.items}
          highlightRule={editor.highlightRule}
          setHighlightRule={editor.setHighlightRule}
          rolesDraft={editor.rolesDraft}
          setRolesDraft={editor.setRolesDraft}
          addRole={editor.addRole}
          removeRole={editor.removeRole}
          addAllow={editor.addAllow}
          addDeny={editor.addDeny}
          removeAllow={editor.removeAllow}
          removeDeny={editor.removeDeny}
          apiRouteConnectionNames={editor.apiConnectionNames}
          addRouteRule={editor.addRouteRule}
          removeRouteRule={editor.removeRouteRule}
          highlightRoute={editor.highlightRoute}
          setHighlightRoute={editor.setHighlightRoute}
        />

        <Tabs
          value={editor.mainTab}
          onValueChange={(v) => editor.setMainTab(v as "permissions" | "behavior")}
          className="gap-0 lg:min-h-0 lg:overflow-hidden"
        >
          <TabsList
            variant="line"
            className="w-full shrink-0 justify-start border-b bg-muted/10 px-5"
          >
            <TabsTrigger value="permissions" className="flex-none">
              Permissions
            </TabsTrigger>
            <TabsTrigger value="behavior" className="flex-none">
              AI Assistant Behavior
            </TabsTrigger>
          </TabsList>

          <TabsContent
            value="permissions"
            className="flex flex-col lg:min-h-0 lg:overflow-hidden"
          >
            <PermissionsExplorer
              draft={draft}
              onUpdate={onUpdate}
              isReadOnly={isReadOnly}
              scope={editor.scope}
              setScope={editor.setScope}
              statusFilter={editor.statusFilter}
              setStatusFilter={editor.setStatusFilter}
              search={editor.search}
              setSearch={editor.setSearch}
              selected={editor.selected}
              setSelected={editor.setSelected}
              hovered={editor.hovered}
              setHovered={editor.setHovered}
              toolCount={editor.toolCount}
              connectionCount={editor.connectionCount}
              api={editor.api}
              items={editor.items}
              resolved={editor.resolved}
              counts={editor.counts}
              grouped={editor.grouped}
              highlightRule={editor.highlightRule}
              allowList={editor.allowList}
              denyList={editor.denyList}
              addAllow={editor.addAllow}
              addDeny={editor.addDeny}
              addMany={editor.addMany}
            />
          </TabsContent>

          {/* BehaviorTab owns the scroll for its own long form, so the panel
              must be a flex column for that flex-1 child to take the height. */}
          <TabsContent
            value="behavior"
            className="flex flex-col lg:min-h-0 lg:overflow-hidden"
          >
            <BehaviorTab draft={draft} onUpdate={onUpdate} isReadOnly={isReadOnly} />
          </TabsContent>
        </Tabs>
      </div>
    </div>
  );
}

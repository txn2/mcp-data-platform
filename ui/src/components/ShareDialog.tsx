import { useMemo, useRef, useState } from "react";
import { ArrowLeft, Check, Link, X } from "lucide-react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { usePortalChannels, useSharePersonas, type PortalChannel, type SharePersona } from "@/api/portal/hooks";
import type { Share, SharePermission } from "@/api/portal/types";
import { AccessRequestsList } from "@/components/access/AccessRequestsList";
import { AddingPeople, EMPTY_DRAFT, type Draft } from "@/components/share/AddingPeople";
import { ChannelView, destinations } from "@/components/share/ChannelView";
import { GeneralAccess, type LinkChange } from "@/components/share/GeneralAccess";
import { PeopleWithAccess } from "@/components/share/PeopleWithAccess";
import { PostedToChannels } from "@/components/share/PostedToChannels";
import { RecipientSearch, type Recipient } from "@/components/share/RecipientSearch";
import { ShareToast, useShareToast } from "@/components/share/ShareToast";
import { useShareData, type ShareData, type ShareKind } from "@/components/share/useShareData";
import { useShareItem } from "@/components/share/useShareItem";
import { DEFAULT_LIFETIME, generalMode, groupShares, linkURL, MODES, pageLink, recipientOf, sharedWith } from "@/components/share/model";
import { useAuthStore } from "@/stores/auth";

export type ShareTarget = {
  type: ShareKind;
  id: string;
  /** What the title names. */
  name?: string;
  /** Who owns the item, for the first row of People with access. */
  ownerEmail?: string;
  /** The tile a post to a channel previews. */
  thumbnail?: string;
};

interface Props {
  /** @deprecated Use `target` instead. */
  assetId?: string;
  target?: ShareTarget;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type View = "share" | "adding" | "channel";

/**
 * ShareDialog answers one question, who can open this item (#2291): the people
 * and groups it is shared with, what anyone else can do with its link, and
 * where it was posted, each changed in place. Adding people and posting to a
 * channel are views of their own, reached from the Share view.
 */
export function ShareDialog({ assetId, target, open, onOpenChange }: Props) {
  const resolved: ShareTarget = target ?? { type: "asset", id: assetId ?? "" };
  const [view, setView] = useState<View>("share");
  const [draft, setDraft] = useState<Draft>(EMPTY_DRAFT);
  const searchOpen = useRef(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const toast = useShareToast();

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          setView("share");
          setDraft(EMPTY_DRAFT);
        }
        onOpenChange(v);
      }}
    >
      <DialogContent
        capped
        showCloseButton={false}
        aria-describedby={undefined}
        className="sm:max-w-[560px]"
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          searchRef.current?.focus();
        }}
        // The first Escape closes the results; the second closes the dialog.
        onEscapeKeyDown={(e) => searchOpen.current && e.preventDefault()}
      >
        <Body
          target={resolved}
          open={open}
          view={view}
          setView={setView}
          draft={draft}
          setDraft={setDraft}
          searchRef={searchRef}
          onSearchOpen={(o) => {
            searchOpen.current = o;
          }}
          toast={toast.show}
          onClose={() => onOpenChange(false)}
        />
        <ShareToast toast={toast.toast} onDismiss={toast.dismiss} />
      </DialogContent>
    </Dialog>
  );
}

interface BodyProps {
  target: ShareTarget;
  open: boolean;
  view: View;
  setView: (v: View) => void;
  draft: Draft;
  setDraft: (d: Draft) => void;
  searchRef: React.RefObject<HTMLInputElement | null>;
  onSearchOpen: (open: boolean) => void;
  toast: (message: string, undo?: () => void) => void;
  onClose: () => void;
}

function Body(props: BodyProps) {
  const { open, view, setView, setDraft, toast } = props;
  const given = props.target;
  const item = useShareItem(given.type, given.id, {
    ...(given.name !== undefined && { name: given.name }),
    ...(given.ownerEmail !== undefined && { ownerEmail: given.ownerEmail }),
    ...(given.thumbnail !== undefined && { thumbnail: given.thumbnail }),
  });
  const target: ShareTarget = { ...given, ...item };
  const data = useShareData(target.type, target.id);
  const personas = useSharePersonas(open).data?.personas ?? [];
  const posts = target.type === "asset" || target.type === "resource";
  const channels = usePortalChannels(open && posts).data?.channels ?? [];
  const options = destinations(channels);
  // When the dialog was opened: what is still in force is read against it.
  const [now] = useState(Date.now);
  const groups = useMemo(() => groupShares(data.shares, now), [data.shares, now]);
  const has = useMemo(
    () => new Set(groups.grants.map((g) => (g.shared_with_persona ? `g:${g.shared_with_persona}` : `p:${recipientOf(g).toLowerCase()}`))),
    [groups.grants],
  );
  const user = useAuthStore((s) => s.user);
  const isFile = target.type === "resource";

  if (view === "channel") {
    return (
      <>
        <Header title="Post to a channel" onBack={() => setView("share")} onClose={props.onClose} />
        <ChannelView
          item={{ type: isFile ? "resource" : "asset", id: target.id, name: target.name ?? "", thumbnail: target.thumbnail, sharer: user?.email ?? "" }}
          options={options}
          onBack={() => setView("share")}
          onPosted={(label) => {
            setView("share");
            toast(`Posted to ${label}`);
          }}
        />
      </>
    );
  }
  if (view === "adding") {
    return (
      <Adding
        {...props}
        target={target}
        data={data}
        personas={personas}
        has={has}
        isFile={isFile}
        onDone={(names) => {
          setDraft(EMPTY_DRAFT);
          setView("share");
          toast(sharedWith(names));
        }}
      />
    );
  }
  return (
    <ShareView
      {...props}
      target={target}
      data={data}
      groups={groups}
      personas={personas}
      has={has}
      now={now}
      channels={channels}
      canPost={posts && options.some((o) => !o.unavailable)}
      ownerYou={!!user && !!target.ownerEmail && user.email?.toLowerCase() === target.ownerEmail.toLowerCase()}
      onPick={(r) => {
        setDraft({ ...EMPTY_DRAFT, chips: [r] });
        setView("adding");
      }}
    />
  );
}

function Header({ title, onBack, onClose }: { title: string; onBack?: () => void; onClose: () => void }) {
  return (
    <div className="flex items-center gap-2 px-6 pt-[22px] pb-1.5">
      {onBack && (
        <Button variant="ghost" size="icon" aria-label="Back" onClick={onBack} className="-ml-2 text-muted-foreground">
          <ArrowLeft className="size-[18px]" />
        </Button>
      )}
      <DialogTitle className="min-w-0 flex-1 truncate text-lg font-semibold">{title}</DialogTitle>
      <Button variant="ghost" size="icon" aria-label="Close" onClick={onClose} className="-mr-2 text-muted-foreground">
        <X className="size-[18px]" />
      </Button>
    </div>
  );
}

function titleOf(t: ShareTarget): string {
  return t.name ? `Share “${t.name}”` : "Share";
}

/** The Share view: the box, who has access, general access, posts, and the footer. */
function ShareView({
  target,
  data,
  groups,
  personas,
  has,
  now,
  channels,
  canPost,
  ownerYou,
  searchRef,
  onSearchOpen,
  onPick,
  setView,
  toast,
  onClose,
}: BodyProps & {
  data: ShareData;
  groups: ReturnType<typeof groupShares>;
  personas: SharePersona[];
  has: Set<string>;
  now: number;
  channels: PortalChannel[];
  canPost: boolean;
  ownerYou: boolean;
  onPick: (r: Recipient) => void;
}) {
  const general = target.type === "asset" || target.type === "collection";
  const mode = generalMode(groups.link);
  const [linkPending, setLinkPending] = useState(false);
  const fail = (err: unknown) => toast(err instanceof Error ? err.message : "That did not save.");

  function changeLink(c: LinkChange) {
    setLinkPending(true);
    const done = () => setLinkPending(false);
    const modeChanged = c.mode !== mode;
    const said = () => modeChanged && toast(`Link access changed to ${MODES[c.mode].label}`);
    if (c.mode === "none") {
      void data.removeLink().then(said, fail).finally(done);
      return;
    }
    const madePublic = c.mode === "public" && mode !== "public";
    void data
      .setLink({
        access_mode: c.mode,
        expires_in: c.expiresIn ?? (madePublic ? DEFAULT_LIFETIME : undefined),
        ...(c.banner !== undefined && { notice_text: c.banner }),
        ...(c.showExpiry !== undefined && { hide_expiration: !c.showExpiry }),
      })
      .then(said, fail)
      .finally(done);
  }

  function remove(share: Share, name: string) {
    void data.revoke(share.id).then(() => {
      const again = () =>
        void data
          .grant({
            ...(share.shared_with_persona ? { shared_with_persona: share.shared_with_persona } : { shared_with_email: recipientOf(share) }),
            permission: share.permission,
            notify: false,
          })
          .catch(fail);
      toast(`Removed ${name}`, again);
    }, fail);
  }

  return (
    <>
      <Header title={titleOf(target)} onClose={onClose} />
      <div className="min-h-0 flex-1 space-y-[22px] overflow-y-auto px-6 pt-3 pb-[22px]">
        <RecipientSearch personas={personas} has={has} picked={new Set()} onPick={onPick} onOpenChange={onSearchOpen} inputRef={searchRef} />
        <PeopleWithAccess
          owner={target.ownerEmail ? { email: target.ownerEmail, you: ownerYou } : undefined}
          grants={groups.grants}
          personas={personas}
          loading={data.isLoading}
          now={now}
          onRole={(s, p: SharePermission) => void data.setPermission(s.id, p).catch(fail)}
          onRemove={remove}
        />
        {general && (
          <GeneralAccess
            mode={mode}
            link={groups.link}
            otherLinks={groups.otherLinks}
            pending={linkPending}
            onChange={changeLink}
            onRemoveOther={(s) => void data.revoke(s.id).catch(fail)}
          />
        )}
        <PostedToChannels posts={groups.posts} channels={channels} onRemove={(s) => void data.revoke(s.id).catch(fail)} />
        <AccessRequestsList target={{ type: target.type, id: target.id }} />
      </div>
      <Footer target={target} link={groups.link} isPublic={mode === "public"} canPost={canPost} onPost={() => setView("channel")} onDone={onClose} />
    </>
  );
}

function Footer({
  target,
  link,
  isPublic,
  canPost,
  onPost,
  onDone,
}: {
  target: ShareTarget;
  link: Share | undefined;
  isPublic: boolean;
  canPost: boolean;
  onPost: () => void;
  onDone: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const url = link ? linkURL(window.location.origin, link) : pageLink(window.location.origin, target.type, target.id);
  const copy = () =>
    void navigator.clipboard
      ?.writeText(url)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      })
      .catch(() => undefined);
  return (
    <div className="flex flex-wrap items-center gap-2 border-t px-6 py-4" data-testid="share-footer">
      <Button variant={isPublic ? "default" : "outline"} className="h-10 rounded-[9px]" onClick={copy}>
        {copied ? <Check /> : <Link />}
        {copied ? "Copied" : "Copy link"}
      </Button>
      {canPost && (
        <Button variant="ghost" className="h-10 rounded-[9px]" onClick={onPost}>
          Post to a channel
        </Button>
      )}
      <span className="flex-1" />
      <Button variant={isPublic ? "outline" : "default"} className="h-10 rounded-[9px]" onClick={onDone}>
        Done
      </Button>
    </div>
  );
}

/** The Adding view: chips, what they can do, the email, and Cancel and Share. */
function Adding({
  target,
  draft,
  setDraft,
  data,
  personas,
  has,
  isFile,
  onSearchOpen,
  setView,
  toast,
  onClose,
  onDone,
}: BodyProps & {
  data: ShareData;
  personas: SharePersona[];
  has: Set<string>;
  isFile: boolean;
  onDone: (names: string[]) => void;
}) {
  const [pending, setPending] = useState(false);
  const cancel = () => {
    setDraft(EMPTY_DRAFT);
    setView("share");
  };

  async function share() {
    setPending(true);
    const results = await Promise.all(
      draft.chips.map((c) =>
        data
          .grant({
            ...(c.kind === "group" ? { shared_with_persona: c.persona } : { shared_with_email: c.email }),
            permission: c.kind === "guest" ? "viewer" : draft.permission,
            notify: draft.notify,
            message: draft.message.trim(),
          })
          .then(
            () => ({ chip: c, error: "" }),
            (err: unknown) => ({ chip: c, error: err instanceof Error ? err.message : "Not shared." }),
          ),
      ),
    );
    setPending(false);
    const refused = results.filter((r) => r.error);
    const shared = results.filter((r) => !r.error).map((r) => r.chip.name);
    if (refused.length === 0) {
      onDone(results.map((r) => r.chip.name));
      return;
    }
    if (shared.length > 0) toast(sharedWith(shared));
    setDraft({
      ...draft,
      chips: refused.map((r) => r.chip),
      refused: Object.fromEntries(refused.map((r) => [r.chip.key, r.error])),
    });
  }

  return (
    <>
      <Header title={titleOf(target)} onBack={cancel} onClose={onClose} />
      <div className="min-h-0 flex-1 overflow-y-auto px-6 pt-3 pb-[22px]">
        <AddingPeople draft={draft} onDraft={setDraft} personas={personas} has={has} isFile={isFile} onOpenChange={onSearchOpen} />
      </div>
      <div className="flex flex-wrap justify-end gap-2 border-t px-6 py-4">
        <Button variant="outline" className="h-10 rounded-[9px]" onClick={cancel}>
          Cancel
        </Button>
        <Button className="h-10 rounded-[9px]" disabled={draft.chips.length === 0 || pending} onClick={() => void share()}>
          Share
        </Button>
      </div>
    </>
  );
}

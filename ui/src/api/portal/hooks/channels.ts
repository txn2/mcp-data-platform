import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// --- Share to a channel (#2241) and a run's posts ---

// ChatTarget is one chat channel a Slack or Mattermost channel's bot is in.
export interface ChatTarget {
  id: string;
  name: string;
  private?: boolean;
}

// PortalChannel is one channel the signed-in person can share to, as the
// notify tool's list action reports it.
export interface PortalChannel {
  name: string;
  kind: string;
  mode: string;
  description?: string;
  carries: string;
  // target is the channel's own chat channel; empty on a channel that
  // stands for its bot, which posts to one of targets.
  target?: string;
  // targets are the chat channels the bot is in, read when the list is.
  targets: ChatTarget[];
  // targets_error says why targets could not be read.
  targets_error?: string;
}

export interface PortalChannelList {
  channels: PortalChannel[];
  // note says why the list is empty.
  note?: string;
}

// ChannelShareResult is what the notify tool answered a channel share with.
export interface ChannelShareResult {
  channel: string;
  queued: number;
  suppressed?: boolean;
  detail: string;
  link?: string;
  access?: string;
  share_id?: string;
  expires_at?: string;
}

// ChannelShareBody is one share to a channel: where it goes, who opens the
// link, and the line posted above the asset.
export interface ChannelShareBody {
  assetId: string;
  channel: string;
  target?: string;
  access_mode: "authenticated" | "public";
  expires_in?: string;
  message?: string;
}

// usePortalChannels lists the channels the signed-in person reaches, with
// each bot's chat channels. It is read only while the dialog that needs it is
// open, and read again each time it opens, so a channel the bot was just
// invited to is offered.
export function usePortalChannels(enabled: boolean) {
  return useQuery({
    queryKey: ["portal-channels"],
    queryFn: () => apiFetch<PortalChannelList>("/channels"),
    enabled,
    staleTime: 0,
  });
}

// useShareToChannel creates a link share on an asset and posts the asset to a
// channel with the share's link. The asset's share list is read again after,
// since the post added a share to it.
export function useShareToChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ assetId, ...body }: ChannelShareBody) =>
      apiFetch<ChannelShareResult>(`/assets/${encodeURIComponent(assetId)}/channel-shares`, {
        method: "POST",
        body: JSON.stringify({
          ...body,
          target: body.target || undefined,
          expires_in: body.expires_in || undefined,
          message: body.message || undefined,
        }),
      }),
    onSuccess: (_res, { assetId }) => {
      void qc.invalidateQueries({ queryKey: ["shares", assetId] });
    },
  });
}

// RunPost is one post a script run made, and what became of it.
export interface RunPost {
  channel: string;
  title: string;
  queued_at: string;
  status: "sent" | "failed" | "queued";
  deliveries: number;
  sent: number;
  failed: number;
  sent_at?: string;
  last_error?: string;
}

// useRunPosts reads the posts one run made.
export function useRunPosts(scriptId: string, runId: string) {
  return useQuery({
    queryKey: ["script-run-posts", scriptId, runId],
    queryFn: () =>
      apiFetch<{ posts: RunPost[] }>(
        `/scripts/${encodeURIComponent(scriptId)}/runs/${encodeURIComponent(runId)}/posts`,
      ),
    enabled: Boolean(scriptId && runId),
  });
}

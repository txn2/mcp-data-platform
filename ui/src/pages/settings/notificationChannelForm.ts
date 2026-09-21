import type {
  NotificationChannel,
  NotificationChannelInput,
} from "@/api/admin/hooks/settings";

// The form state one notification channel is edited through, and the rules
// deciding which fields its kind can use (#1720).
//
// It is a module of its own so the card (a list) and the editor (a form) can
// each be read on its own while agreeing exactly on what a kind needs — which
// is the thing that must not drift, since the API refuses a shape that does
// not match.

// kindLabel is how each kind is named to an administrator.
export const kindLabel: Record<string, string> = {
  mattermost: "Mattermost",
  webhook: "Incoming webhook",
  email: "Email list",
};

// kindHelp says what each kind needs, in the terms the administrator will go
// and find: a Mattermost channel id is a value they look up, not something
// they can guess.
export const kindHelp: Record<string, string> = {
  mattermost:
    "Posts through the Mattermost REST API. The api connection holds the bot token and the site URL; the target is the channel id.",
  webhook:
    'Posts one {"text": …} body to an incoming-webhook URL, which Slack and Mattermost both accept. The whole URL is the api connection\'s base URL, since a webhook URL is a credential written as an address.',
  email:
    "Delivers to the addresses below through the deployment's mail server. It names no connection, so anyone who can send at all can send to it.",
};

// ChannelFormState mirrors the PUT body, with the numeric field kept as a
// string while typing so it can be emptied without snapping back to a default.
export interface ChannelFormState {
  name: string;
  kind: string;
  description: string;
  enabled: boolean;
  connection: string;
  target: string;
  recipients: string[];
  mode: string;
  repeat_after: string;
  max_per_hour: string;
}

export const blankChannelForm: ChannelFormState = {
  name: "",
  kind: "mattermost",
  description: "",
  enabled: true,
  connection: "",
  target: "",
  recipients: [],
  mode: "immediate",
  repeat_after: "",
  max_per_hour: "",
};

// usesConnection and usesTarget are the two rules a kind's fields follow. An
// email channel names no connection because its transport is the deployment's
// mail server; a webhook names no target because its URL is where the message
// lands.
export function usesConnection(kind: string): boolean {
  return kind !== "email";
}

export function usesTarget(kind: string): boolean {
  return kind === "mattermost";
}

export function channelFormFrom(ch: NotificationChannel): ChannelFormState {
  return {
    name: ch.name,
    kind: ch.kind,
    description: ch.description ?? "",
    enabled: ch.enabled,
    connection: ch.connection ?? "",
    target: ch.target ?? "",
    recipients: ch.recipients ?? [],
    mode: ch.mode,
    repeat_after: ch.repeat_after ?? "",
    max_per_hour: String(ch.max_per_hour ?? ""),
  };
}

// channelInputFrom drops the fields the chosen kind cannot use, so a kind
// switched in the form does not save a leftover from the one before it —
// which the API would refuse, naming a field the administrator can no longer
// see.
export function channelInputFrom(form: ChannelFormState): NotificationChannelInput {
  return {
    kind: form.kind,
    description: form.description,
    enabled: form.enabled,
    connection: usesConnection(form.kind) ? form.connection : "",
    target: usesTarget(form.kind) ? form.target : "",
    recipients: form.kind === "email" ? form.recipients : [],
    mode: form.mode,
    repeat_after: form.repeat_after || undefined,
    max_per_hour: form.max_per_hour ? Number(form.max_per_hour) : undefined,
  };
}

---
description: "The Inbox: everything addressed to you — threads on your work, and what the platform has sent you."
---

# Inbox

The section is **Inbox**: one place for everything addressed to you. It holds the
feedback threads on your work and, since #1798, the notification history that
used to sit on the settings screen beside the preferences that decide what gets
sent. The notification *preferences* stay in Settings, where a setting belongs.

Feedback lets the people who review your work, including subject-matter experts and stakeholders who do not use an agent, leave structured corrections and questions on the things you share with them, instead of relaying that feedback over email.

Feedback is organized into **threads**. A thread targets one asset, collection, prompt, or knowledge page, or it lives on a **standalone channel** for general feedback not tied to a single object. Each thread has a kind (comment, question, correction, rating, approval, rejection, or suggestion), a status (open, answered, resolved, won't fix, acknowledged), an optional `requires_resolution` flag, and a timeline of events (the opening message plus replies and status changes). A thread can be anchored to a specific selection within the target so a correction like "we don't use that term" stays pinned to the place it refers to, along with the version it was raised against. Standalone-channel threads are visible to every signed-in user; feedback on an asset, collection, or prompt is visible to people who can already view that object; knowledge pages are org-shared, so any signed-in user can read and add feedback on them.

Feedback you have not seen also reaches you outside the portal: an
[email notification](../server/notifications.md) when a thread event lands, and a
[session-start notice](../server/session-notices.md) on the first `platform_info` call of
an agent session, which lists the unresolved threads other people left on assets
you own.

## The feedback panel

Open the **Feedback** button in an asset, collection, prompt, or knowledge-page viewer to open the feedback panel beside the item, or under it on a screen narrower than 1024px. Nothing lies over the item while it is open, so you can keep scrolling a document or paging a deck as you write, and what you have typed stays. The arrow keys page a deck unless your cursor is in a text box. It lists the threads on that item with their kind, status, author, and activity, and a header counts how many are open and how many still need resolution. Selecting a text passage in markdown or plain-text content (an asset, a prompt, or a knowledge page) before opening **New** lets you anchor your feedback to that selection. In the Knowledge hub, each knowledge-page card shows an open-thread badge so you can see where feedback is waiting.

![Asset feedback panel](../images/screenshots/light/user-asset-feedback-light.webp#only-light)![Asset feedback panel](../images/screenshots/dark/user-asset-feedback-dark.webp#only-dark)

Opening a thread shows its full timeline: each message is a card with its author's name, and each status or visibility change is a thin line between them ("Name marked Answered"). Anyone can reply; the item's owner, an editor, or an admin can change the status or delete the thread. The status is a button in the thread's header that opens the statuses grouped into **Still open** (Open, Answered, Acknowledged) and **Closes the thread** (Resolved, Won't fix), each with a line saying what it means.

Picking a status shows a notice with **Undo**, and the line the change wrote carries the same Undo. Undo restores the previous status and removes the line, so the thread reads as if the change never happened; nobody is notified of either, and the audit log records both (`POST /api/v1/portal/threads/{id}/events/{eventID}/undo`). It is offered to the person who made the change, for 10 minutes, and only until someone else adds to the thread; after that a change is reverted by picking the earlier status, which writes a line of its own.

The reply box opens at least 150px tall, can be resized or expanded, has a formatting toolbar (bold, italic, code, link, list, mention) over the Markdown a reply is written in and a Preview, keeps a draft while the thread is open, and offers **Reply and resolve** to whoever may change the thread's status.

![Feedback thread detail](../images/screenshots/light/user-asset-feedback-detail-light.webp#only-light)![Feedback thread detail](../images/screenshots/dark/user-asset-feedback-detail-dark.webp#only-dark)

## Internal and shared threads

A thread is **Internal** or **Shared** (#2227). An internal thread is for the organization: a person of an [external persona](../personas/overview.md#external-personas) never sees it, in a list, a count, the sign-off, or by its address, and a mention or notification of it reaches nobody external. A shared thread is seen by everyone who can open the item, external people included. The composer opens on **Internal**, the thread list and header show which a thread is, and the item's owner, an editor or an administrator can change it later; the change is recorded on the timeline. A thread an external person opens is always shared, and an external person cannot change one.

## Access requests

When someone asks for access to an item you own, the request is listed under **Access requests** at the top of the Inbox, with **Approve as viewer**, **Approve as editor** and **Decline**. See [Requesting access](assets.md#requesting-access).

## Starting a thread

A thread is its messages, like a comment on a document (#2513). **New** asks for the kind and the message, and no title. Wherever a thread is listed (the item's feedback panel, Recent activity, the worklists, notification emails, and the session-start notice an agent receives) it is shown by who wrote it, the first line of its first message, and the item it is on. A thread opened before titles were dropped keeps its stored title as its heading. A thread whose first message was deleted is listed by its next message with text, and one with no text at all (a rating left without words) by its kind.

The message box and the reply box start four lines high, grow with what you type up to a limit and then scroll, and take the full width of the panel. **Cmd+Enter** (macOS) or **Ctrl+Enter** sends; **Enter** starts a new line.

## Writing a message

A thread's messages are Markdown (#2214): emphasis, `code`, lists, headings, tables and links render as such, and a single line break is kept as written. Raw HTML is not rendered and a link with a script address is not followed. **Preview** above the composer shows the message as it will read, with the mentions that will be delivered as chips; **Write** goes back to editing. A notification email renders the message the same way.

## Mentioning a teammate

Type `@` anywhere in a feedback message or reply to address someone directly. The composer suggests people as you type and inserts the person as `@marcus.johnson(example.com)`, which reads as a name in the thread and is stored as an address so it keeps working when someone's display name changes.

![Mentioning a teammate](../images/screenshots/light/user-asset-feedback-mention-light.webp#only-light)![Mentioning a teammate](../images/screenshots/dark/user-asset-feedback-mention-dark.webp#only-dark)

The suggestions are the people who can already open the item being discussed: its owner and everyone it is shared with, directly or through a collection. Knowledge pages and the standalone channel are open to every signed-in user, so any known user may be mentioned there. This is deliberate. A mention sends the item's title and an excerpt of the comment by email, so it may only go to someone who could open the item anyway.

Whether a mention is delivered is decided by the same rule that decides who can open the item (#2512): a person who opens it through a share to their persona, an email share, a collection share or as an administrator can be mentioned, judged by the roles they last signed in with. A person the item is not shared with by any route is not.

You can still type an address by hand. If it belongs to someone without access, the composer says so while you are writing and the mention posts as ordinary text: it is not recorded, not rendered as a chip, and delivers nothing. Share the item with them first, then mention them.

Being mentioned is its own notification category, separate from general comment activity, so someone who muted thread chatter still hears when a comment names them. The **Mentions of me** tab in the feedback inbox lists every thread where a comment addressed you.

## Who hears about a reply

A message notifies the item's owner, the people it is shared with, and everyone the thread involves: its author, everyone who has written in it, and everyone it mentions (#2512). Nobody is told of their own message, and a person who can no longer open the item is told nothing. Every notification is also in the **Notifications** tab of the Inbox, with an unread count on the tab and on the Inbox in the sidebar, so a reply or a mention reaches people even where email is not configured or does not arrive.

A thread on the General (standalone) channel has no item, so no owner and nobody it is shared with. Who hears about it is the General feedback list (#2603): the people of the personas it names (everyone in the user directory whose roles map to one of them, which leaves out someone who only uses an API key), addresses it names such as a team list, and notification channels. They are told of a new post and of every reply, as an item's owner is; a channel gets the new post only. The email links to the thread on the Feedback page (`/portal/feedback#thread-<id>`). The list can also send the author a "Your feedback was received" email, which never names the recipients and is sent only when someone was told. A new post that reaches nobody is logged as a warning. Until an administrator saves a list, it is `portal.feedback.standalone_recipients`, and with that unset the administrator persona. An administrator sets it in Admin > Settings > General feedback ([Settings](admin-settings.md#settings)); it is read and saved at `GET`/`PUT /api/v1/admin/settings/general-feedback` (the `general_feedback` section); `GET /api/v1/portal/feedback/general` answers only whether anyone is told. A thread on an item whose owner cannot be found (a deleted item, a page with no recorded author) falls back to the same list, each person still held to the item's access.

## Editing and deleting a message

The author of a message can edit it or delete it from the thread (#2514). An edited message shows **edited** and when; the text it had before is kept. A deleted message shows **Message deleted** in its place, so the replies after it still read in order, and its text is no longer returned anywhere: the thread, the lists, search and the notifications. Deleting the opening message of a thread nobody answered deletes the thread.

The item's owner or editor and an administrator can delete any message on it; only the author can edit one. Only messages can be changed: an approval, rejection, rating, status change or validation result records a decision and is never edited or deleted. Adding a mention by editing notifies the person newly named, once.

Agents leave feedback through the same path: `manage_feedback action=create` opens a thread through `POST /api/v1/portal/threads` as the calling person, so who may comment where is the portal's rule (#2371), and a reply written with the `manage_feedback` tool carries mentions and fires the same notifications as one written in the portal, on deployments that run the HTTP server. Email delivery lives with that server, so a stdio-only deployment stores the reply but sends nothing.

## Turning feedback into knowledge

A correction or suggestion is only useful if it can change something. When you have **apply_knowledge** access, an unresolved correction or suggestion thread shows a **Capture as insight** action in its detail view. Capturing it creates a pending insight from the thread (its title and first comment) that enters the review queue alongside insights captured by agents, and resolves the thread with a link to that insight. If the title and first comment together are over the 4,000-byte limit for an insight, the panel shows the text in an editable field with a live byte count, and you shorten it before capturing; nothing is cut for you. When the server refuses a capture, the panel shows its reason. From there the normal apply_knowledge review and promote/apply pipeline takes over: once the insight is promoted to a knowledge page or applied to the catalog, the thread's knowledge chain shows the resulting change, closing the loop for both the reviewer and the person who raised the feedback. This is how feedback on any content becomes durable, reviewed knowledge rather than a dead-end comment.

The **Feedback** page in the sidebar is the standalone channel for general feedback. The My Assets and Collections lists show an open-thread badge on items you own so you can see at a glance where feedback is waiting.

![Feedback channel](../images/screenshots/light/user-feedback-light.webp#only-light)![Feedback channel](../images/screenshots/dark/user-feedback-dark.webp#only-dark)

## Leaving feedback through a public link

When you share an asset or collection with a public link, an anonymous visitor can view it and sees a **Sign in to leave feedback** prompt. Signing in through that link, when the visitor has no prior share for the item, grants them a viewer share automatically so the item appears in their portal and they can leave feedback. An existing editor is never downgraded to a viewer by this flow.

Once they hold that share, opening the link takes them straight to the item in their portal, where the feedback panel is. The public page is still one click away from there, under **Shared page**.

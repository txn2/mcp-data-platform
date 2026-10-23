# IMAP Mailbox

An `imap` connection reads a dedicated platform mailbox, for example `platform@company.example`, which people cc on the threads the platform should know about: client threads, vendor notices, automated reports. People can then ask about those threads, and managed scripts can process new mail on a schedule.

Mail is the most hostile input the platform takes: anyone can send to the address, and a body reaches a model's context as it stands. Most of the kind is what it refuses, and everything below is enforced by the platform rather than left to a model's judgment.

## What the platform shows

1. **Folders.** A connection reads only the folders it names; there is no all-folders default. The intended setup is a server-side rule that files mail cc'd to the platform address from known senders into one folder.
2. **Sender allowlist.** A message whose From address is not on `allow_senders` (addresses and `@domain` entries; a domain entry does not admit its subdomains) is never returned by any tool: not shown, summarized or quoted. `mail_search` reports how many it withheld (`hidden_not_allowed`), so an administrator knows mail is arriving.
3. **Authenticated senders.** With `require_auth_pass` (the default), an allowed From is trusted only when the receiving mail server recorded an SPF or DKIM pass aligned with it (relaxed alignment under the public suffix list), or a DMARC pass. Only the topmost `Authentication-Results` header carrying `trusted_authserv_id` is read, never one a sender could have added under another id. Anything else counts as `hidden_unauthenticated`. This is what stops a forged `From: ap@vendor.example` from passing.
4. **Body as data.** Every header and body field the sender controls is returned under `external`, apart from the platform's own fields (uid, folder, date, size, message id, thread references, `from_address`, `sender_auth`, attachments), and every result carries a `notice` saying what `external` is. HTML is converted to the text a reader sees: scripts, styles, comments and every element hidden from the reader (`hidden`, `aria-hidden`, `display:none`, `visibility:hidden`, zero font size, opacity, width or height) are dropped, images become their alt text, and no remote content is fetched or link followed. Zero-width, bidirectional and other invisible characters are removed from everything. Quoted history under a reply is collapsed unless `include_quoted` is set.
5. **Read-only.** Folders are opened with EXAMINE and bodies fetched with `BODY.PEEK`, so nothing is marked seen, and there is no STORE, COPY, MOVE, EXPUNGE, APPEND or send. Plain IMAP is refused: the session is IMAPS or STARTTLS.
6. **Limits.** `max_results` per call, `max_message_bytes` and `max_attachment_bytes`, at most 2,000 messages' headers read per search, and at most 500 mail calls per managed-script run.
7. **Audit.** Every call is audited like any other tool call, with its folder and uid.

## Tools

| Tool | Does |
|---|---|
| `mail_search` | Search a folder (from, to, cc, subject, text, since, before, unseen) and return headers only, newest first. With `since_uid` and `uid_validity` from the last run it returns only newer mail, oldest first, and `highest_uid` is the next watermark; `uid_validity_changed` says the folder was renumbered and the watermark was ignored |
| `mail_message` | One message: headers and the body as plain text, its attachment list. A message from a sender the connection does not trust is refused, saying why and nothing it contains |
| `mail_thread` | The thread a message belongs to, by Message-ID, In-Reply-To and References, oldest first with bodies; withheld messages are counted |
| `mail_attachment` | Save one attachment of a trusted message into the caller's resource library (default folder `mail/<connection>`), with its own content type, so the file tools (PDF text, table registration) read it. Offered only where the deployment has a resource library |

## Managed scripts that act on mail

A script that reads mail and then writes (a notification, an asset, an API POST, `trino_execute`, an output) is a script a sender can steer by what they write. A script's `mail_actions` setting says its owner meant that: with it off (the default), a run that has called any `mail_*` tool is refused every later write-class tool call (as `internal/toolwrite` classifies it) and every `platform.export` and `platform.publish_data`, and the refusal says what to turn on. A run that never reads mail is unaffected. The setting is set with `manage_script` `create` or `update` (`mail_actions: true`), rehearsed with `run_draft`, shown by `get` and in the script's contract, and toggled on the script's page under "Reads mail and acts on it" (`PUT /api/v1/portal/scripts/{id}/mail-actions`, owner or administrator). A draft that persists nothing is not refused.

## Configuration

```yaml
toolkits:
  imap:
    instances:
      platform-mail:
        host: imap.example.com
        username: platform@example.com
        password: "{{secret:platform-mailbox}}"
        folders: [Platform]
        allow_senders: ["@vendor.example", "reports@partner.example"]
        trusted_authserv_id: mx.example.com
```

| Key | Meaning |
|---|---|
| `host`, `port`, `tls` | `tls` (IMAPS, port 993, the default) or `starttls` (port 143). Plain IMAP is refused |
| `username`, `password` | An app password; `{{secret:<name>}}` names a stored secret, read each time a session opens. Required |
| `folders` | The folders the connection may read. Required |
| `allow_senders` | Addresses and `@domain` entries whose mail is visible. Required |
| `require_auth_pass` | Trust an allowed sender only on an aligned SPF, DKIM or DMARC pass. Default true |
| `trusted_authserv_id` | The receiving server's authserv-id, the first word of the `Authentication-Results` header it adds. Required while `require_auth_pass` is on |
| `max_results` | Most messages one call returns (1-500). Default 50 |
| `max_message_bytes`, `max_attachment_bytes` | Default 10 MiB and 25 MiB |
| `tls_ca_bundle_pem` | PEM certificates to trust for a private CA |
| `connect_timeout`, `call_timeout` | Default 15s and 60s |

Connections are usually saved in the portal under Admin > Connections (kind IMAP) or through `PUT /api/v1/admin/connection-instances/imap/{name}`; the kind is enabled automatically. The connection test logs in and opens each folder read-only, reporting how many messages each holds. Sending mail is out of scope: notification channels cover outbound.

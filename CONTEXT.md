# xmail

xmail is an email-as-a-service backend: it keeps several email accounts, sends mail on their behalf, and reads their mailboxes over standard protocols, exposing the same operations over a REST API and an MCP server.

## Language

**Account**:
One configured mailbox xmail can act on — a name, an email address, a username, and the connection settings for the protocols it supports. An Account never carries its Credential.
_Avoid_: user, profile, mailbox

**Mailer protocol**:
One of the wire protocols xmail speaks for an account: SMTP (send), IMAP (fetch, check, mark-read), or POP3 (fetch only). An account configures any subset of them.

**Connection**:
The host, port, and TLS mode for one Mailer protocol on an account.
_Avoid_: endpoint, server config

**Credential**:
The password or app-password for an account. Stored encrypted; never returned to a caller.
_Avoid_: secret, token

**Message**:
The metadata of one email in a mailbox — its UID, subject, sender, recipient, date, read state, and attachment filenames. Never the body.
_Avoid_: email, mail item

**Folder**:
A named mailbox on the server. IMAP has many; POP3 has none, so its only Folder is INBOX.
_Avoid_: directory, label

**Fetch window**:
A slice of a mailbox's messages, newest-first: the most recent N, skipping the first M.
_Avoid_: page, offset/limit

**Message cache**:
xmail's local store of Message metadata, keyed by (account, protocol, folder), so a repeated fetch need not re-dial the mail server.
_Avoid_: store, index, spool

**Coverage**:
How many of a mailbox's newest messages the Message cache is known to hold contiguously from the top. A short fetch (fewer messages than it asked for) proves where the mailbox ends, so it moves Coverage to that point and drops anything cached past it.
_Avoid_: cache size, depth

**Exhausted**:
As of the last dial, the whole mailbox is in the Message cache, so a Fetch window inside the covered prefix is answerable from it without dialing. A short fetch that carries messages sets this; an empty fetch below the top does not (it only bounds the mailbox); a later full page reaching past the known end clears it (the mailbox grew). A fetch that starts below the cached window is ignored.

**Verified from**:
The lowest position from the top of the mailbox that the most recent dial vouched for: 0 after a top fetch, the fetched page's own offset after a deeper one. A Fetch window that starts below it dials, because the rows there were written by an older dial and the mailbox may have changed at the top since.

**Check**:
A poll of an account's mailbox that reports the server's unread count plus how many of the most recent Messages are not yet in the Message cache.
_Avoid_: poll, refresh

**Check result**:
The outcome of a Check: how many messages are unread, and how many of the most recent are not yet in the Message cache.

# Future TUI improvements

This document records ideas for later work, not features available today. Reaction display is implemented: current Tapbacks appear beneath their original messages and update when the database changes. Sending reactions and native inline replies is not implemented.

## Message selection and actions

The message pane currently scrolls text, so actions cannot target a selected message.

Add a visible message cursor and an actions menu for Reply, View thread, View reactions, and attachment actions. Bind selection to the message GUID and body-part index, not a position in the displayed list. New messages, reactions, and refreshes must not move an action to another message.

Keep scrolling available separately from message navigation. Avoid taking over `r`/`R` (refresh), `Ctrl+R` (failed-message recovery), or `p` (image preview) without an explicit shortcut change. An `a` actions menu is one option to evaluate.

Completion requirements:

- Users can identify the selected message and act without moving focus to the composer first.
- Selection survives new messages and list refreshes.
- Attachment actions target the selected message or part, rather than the latest image in the conversation.
- Missing or deleted targets disable the affected action with a clear explanation.

## Thread browsing

Load native reply/thread metadata and show a short parent preview on each reply. A thread view should show the root message and its replies, with a clear way back to the main conversation.

Fetch a parent by GUID when it is outside the loaded history window. Distinguish a reply's direct parent from the root of its thread when both are available. Use the same text and attachment decoding as ordinary history.

Completion requirements:

- Replies show useful context even when the parent is older than the latest 100 messages.
- Opening and closing a thread preserves the main timeline's position and draft.
- Missing parents have an explicit placeholder; lookup errors are not presented as missing data.
- Live updates stay in the correct thread and conversation.

## Reply composer

Show a banner above the composer with the target's author and a short preview. Capture the chat GUID, message GUID, and part before starting a send. Keep reply targets with their per-chat drafts, or cancel them explicitly when switching conversations.

Quoted-text replies are a possible first step with the existing sender. They must be a separate, clearly labelled action: an ordinary message containing a quote does not become an Apple inline reply.

Completion requirements:

- Users can see and cancel the reply target without losing their typed text.
- Refreshes and chat switches cannot silently change the target.
- Failed sends retain both the text and intended target for review.
- Unsupported native replies fail before dispatch rather than silently becoming ordinary messages.

## Reaction details and native actions

The current display shows reaction labels, people, and part numbers. Once messages are selectable, add a reaction-details view and consider grouped counts for busy conversations. Keep custom emoji intact; show an explicit label when emoji or sticker artwork is unavailable.

A reaction picker should distinguish adding, replacing, and removing the current user's reaction. Do not enable sending until the backend can target the exact message and part.

The current `imsg` documentation describes two different paths:

- [Standard Tapback UI automation](https://github.com/OpenClaw/imsg/blob/main/docs/send.md) does not need private-framework injection, but cannot guarantee the selected message's GUID. Window focus and user interaction affect its target.
- [GUID-targeted native replies and Tapbacks](https://github.com/OpenClaw/imsg/blob/main/docs/bridge.md) use an optional private-framework bridge. Its [setup requirements](https://github.com/OpenClaw/imsg/blob/main/docs/advanced-imcore.md) include disabled system protections and macOS-specific limitations. This should not be the default backend.

Recheck those capabilities before choosing a backend. An explicit manual handoff to Messages is safer than pretending an unreliable target is supported. Never write to `chat.db` to send a reply or reaction.

The smallest capability experiment is a macOS prototype that targets a known GUID and part. Check duplicate text, multipart messages, a parent not loaded in the Messages window, incoming activity, focus changes, removal/replacement, permissions, and a timeout after possible dispatch. Verify the target and result in Messages; an automation acknowledgment or a local row is not proof of remote delivery. Do not retry an uncertain outcome automatically.

## Other browsing and composing ideas

- Searchable conversations and in-chat search, with a way to jump to a result and back.
- Loading older history without losing the scroll position or draft.
- Multiline composing with an explicit send shortcut instead of overloading Enter.
- Pending, failed, and uncertain send states. Keep recovery separate from retry; an uncertain send must be checked before resending.
- Contextual help showing only actions available in the focused panel.

## Suggested order

1. Message selection and contextual actions.
2. Thread metadata, parent previews, and focused thread browsing.
3. Explicit quoted-reply mode and target-aware draft recovery.
4. History navigation, search, and richer composing.
5. A separately evaluated native-action backend, only after exact-target behavior is proven.

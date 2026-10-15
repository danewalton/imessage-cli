# iMessage CLI

A command-line tool for reading and sending iMessages on macOS.

## Features

- **List conversations** - View recent iMessage conversations
- **Read messages** - Read messages from any conversation
- **Send messages** - Send iMessages from the command line
- **Interactive chat** - Real-time chat mode with a contact
- **Search** - Search through your message history
- **TUI** - Full terminal user interface with live updates and message reaction badges

## Requirements

- macOS (uses the Messages app and iMessage database)
- Go 1.24 or later
- Full Disk Access permission for Terminal/your terminal emulator

## Building

```bash
go mod download
go build -o imessage ./cmd/imessage
```

## Installation

```bash
# Build and install to your GOPATH/bin
go install ./cmd/imessage

# Or build locally
go build -o imessage ./cmd/imessage
```

## Usage

### List recent conversations

```bash
imessage list
imessage ls
imessage l
```

### Read messages from a conversation

```bash
# By conversation number from list
imessage read 1

# By phone number
imessage read "+1234567890"

# Specify number of messages
imessage read 1 -n 50

# Use the stable Chat ID shown by list
imessage read chat:42
```

`read` and `chat` accept a list index, a phone number, an email, or a stable `chat:<id>` reference. Bare numbers of up to three digits are list indexes; longer numbers and `+`-prefixed numbers are phone addresses. Use a quoted `"#1"` for an explicit index. List positions change when new messages arrive, so use `chat:<id>` for saved commands.

Phone and email lookups use exact matches and supported phone-number variants, never substring matches. If more than one direct conversation matches, select its Chat ID from `list`.

### Send a message

```bash
imessage send "+1234567890" "Hello from the command line!"

# Skip confirmation
imessage send "+1234567890" "Hi" -y

# Reply to an existing conversation, including a group
imessage send chat:42 "Hello everyone!"
```

Replies to existing conversations use their exact Messages chat ID. Groups are not selected by their display name or by one participant. The reply tip printed by `read` uses a stable Chat ID.

### Interactive chat mode

```bash
imessage chat 1
imessage chat "+1234567890"
imessage chat chat:42
```

### Search messages

```bash
imessage search "meeting"
imessage search "project" -n 50
```

Search matches literal text without regard to case, including decoded rich-text messages. `%` and `_` are literal characters, not wildcards. Search reads messages newest-first until it finds the requested number of matches; rare searches over a large history can take longer because rich-text bodies must be decoded.

### Launch TUI (Terminal User Interface)

```bash
imessage tui
imessage ui
imessage watch
```

### Check status

```bash
imessage status
```

Database access and query failures are reported as errors rather than empty accounts or zero statistics. `status` exits with a nonzero status when its database checks fail.

## TUI Controls

| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Navigate/scroll |
| `Enter` | Select conversation |
| `Tab` | Switch between panels |
| `h/←` | Go back to conversations |
| `l/→` | Go to messages |
| `i` | Start typing a message |
| `Enter` in the input field | Send the current draft |
| `Esc` in the input field | Return to messages without discarding the draft |
| `Ctrl+R` | Recover a failed message for the selected conversation, when the input is empty |
| `r` | Refresh |
| `g` | Go to top (messages) |
| `G` | Go to bottom (messages) |
| `q` | Quit |

Drafts stay with their conversations when you switch chats or the list reorders. A pending send does not accept a second message; the second draft stays in the input. Failed messages are restored only if you have not edited or switched the composer. Otherwise, select the original conversation and use `Ctrl+R` with an empty input to recover the failed message. Drafts and failed messages are kept only for the current TUI session.

### Message reactions

Current Tapbacks appear beneath their original messages, with the reacting person's name. The six standard reactions use emoji labels; custom emoji is shown when its metadata is available. Otherwise it is labelled `Emoji`. Sticker Tapbacks are labelled `Sticker`; their artwork is not decoded.

Reactions to multipart messages include a 1-based body-part number; reactions to an app bubble are marked `bubble`. These numbers refer to Messages' body parts, not necessarily the order of the displayed attachment filenames.

Reaction additions, replacements, removals, and deleted reaction rows update live. Reaction-only refreshes keep the message pane's scroll position and leave the composer unchanged. Reaction events do not appear as separate messages or consume the history limit. `read` and interactive `chat` also show current reaction summaries; search excludes reaction event text.

This feature only reads the database and needs no new permission. Older databases without reaction metadata still load ordinary history. Sending reactions and native threaded replies is not supported.

See [Future TUI improvements](docs/tui-roadmap.md) for message selection, threads, reply composing, and the native-action backend tradeoffs.

### Private debug logging

```bash
imessage tui --debug
```

On macOS, the log is `~/Library/Caches/imessage-cli/tui.log`. The log directory and file are accessible only to your user. Debug logging records event types and focus changes, not typed message characters. The old `/tmp/imessage-tui.log` is no longer used; remove any old copies if they contain private drafts.

## Permissions

This tool requires access to:

1. **iMessage Database** (`~/Library/Messages/chat.db`) - Grant Full Disk Access to your terminal
2. **Contacts Database** (`~/Library/Application Support/AddressBook/`) - For resolving contact names
3. **Messages App** - Via AppleScript for sending messages

To grant Full Disk Access:
1. Open System Preferences → Security & Privacy → Privacy
2. Select "Full Disk Access"
3. Add your terminal application (Terminal.app, iTerm2, etc.)

## Project Structure

```
imessage-cli/
├── cmd/
│   └── imessage/
│       └── main.go           # Entry point
├── internal/
│   ├── cli/
│   │   └── cli.go            # CLI commands
│   ├── database/
│   │   ├── database.go       # iMessage database operations
│   │   └── contacts.go       # Contact resolution
│   ├── sender/
│   │   └── sender.go         # AppleScript message sending
│   ├── tui/
│   │   └── tui.go            # Terminal user interface
│   └── watcher/
│       └── watcher.go        # Real-time message watching
├── go.mod
├── go.sum
└── README.md
```

## Dependencies

- [spf13/cobra](https://github.com/spf13/cobra) - CLI framework
- [mattn/go-sqlite3](https://github.com/mattn/go-sqlite3) - SQLite driver
- [rivo/tview](https://github.com/rivo/tview) - TUI framework
- [gdamore/tcell](https://github.com/gdamore/tcell) - Terminal handling

## Differences from Python Version

This Go version is a complete rewrite with:
- Single binary distribution (no Python dependency)
- Faster startup time
- Native concurrency with goroutines
- Uses tview instead of curses for the TUI

## License

Same license as the parent project.

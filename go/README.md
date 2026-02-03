# iMessage CLI (Go Version)

A command-line tool for reading and sending iMessages on macOS, rewritten in Go.

## Features

- **List conversations** - View recent iMessage conversations
- **Read messages** - Read messages from any conversation
- **Send messages** - Send iMessages from the command line
- **Interactive chat** - Real-time chat mode with a contact
- **Search** - Search through your message history
- **TUI** - Full terminal user interface with live updates

## Requirements

- macOS (uses the Messages app and iMessage database)
- Go 1.21 or later
- Full Disk Access permission for Terminal/your terminal emulator

## Building

```bash
cd go
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
```

### Send a message

```bash
imessage send "+1234567890" "Hello from the command line!"

# Skip confirmation
imessage send "+1234567890" "Hi" -y
```

### Interactive chat mode

```bash
imessage chat 1
imessage chat "+1234567890"
```

### Search messages

```bash
imessage search "meeting"
imessage search "project" -n 50
```

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

## TUI Controls

| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Navigate/scroll |
| `Enter` | Select conversation |
| `Tab` | Switch between panels |
| `h/←` | Go back to conversations |
| `l/→` | Go to messages |
| `i` | Start typing a message |
| `r` | Refresh |
| `g` | Go to top (messages) |
| `G` | Go to bottom (messages) |
| `q` | Quit |

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
go/
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

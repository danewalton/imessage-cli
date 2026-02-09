# Architecture Document — iMessage CLI

## Overview

iMessage CLI is a macOS-native command-line tool written in Go that provides read and write access to the iMessage ecosystem. It reads messages directly from the macOS iMessage SQLite database (`~/Library/Messages/chat.db`) and sends messages via AppleScript automation of the Messages app. The application offers both traditional CLI subcommands and a full interactive terminal user interface (TUI).

## High-Level Architecture

```
┌─────────────────────────────────────────────────────┐
│                   cmd/imessage/main.go               │
│              (Entrypoint & DB lifecycle)              │
└──────────────────────┬──────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────┐
│                  internal/cli                        │
│    (Cobra command tree — dispatches to subsystems)    │
└───┬──────────┬──────────┬──────────┬────────────────┘
    │          │          │          │
    ▼          ▼          ▼          ▼
 database   sender     watcher     tui
```

The application follows the standard Go project layout with `cmd/` for the binary entrypoint and `internal/` for private packages that make up the core logic.

## Package Breakdown

### `cmd/imessage/main.go` — Entrypoint

The entrypoint is minimal. It:

1. Defers `database.CloseDB()` to ensure the shared SQLite connection pool is closed on exit.
2. Delegates to `cli.Execute()` which runs Cobra's root command.

### `internal/cli` — Command-Line Interface

**Purpose:** Defines all CLI subcommands and their presentation logic.

**Framework:** [Cobra](https://github.com/spf13/cobra) for command parsing, flag handling, and help generation.

**Commands:**

| Command | Aliases | Description |
|---------|---------|-------------|
| `list` | `ls`, `l` | List recent conversations with formatted table output |
| `read` | `r`, `view` | Read messages from a conversation (by index or phone number) |
| `send` | `s` | Send a message with optional confirmation prompt |
| `chat` | `c` | Interactive REPL-style chat loop with a contact |
| `search` | `find`, `grep` | Full-text search across message history |
| `status` | — | Show database accessibility, Messages app state, and statistics |
| `tui` | `ui`, `watch` | Launch the full terminal user interface |
| `version` | — | Print version string |

The default action (no subcommand) runs `list` with 20 conversations.

**Design notes:**
- All terminal output uses ANSI color codes with a `colored()` helper that detects whether stdout is a TTY, ensuring clean output when piped.
- Conversation references are index-based (e.g., `imessage read 3`) or identifier-based (e.g., `imessage read "+1234567890"`), and the CLI resolves these uniformly before querying.

### `internal/database` — Data Access Layer

**Purpose:** All reads from the iMessage SQLite database and the macOS AddressBook database.

**Files:**

- **`database.go`** — Core database operations: connection management, message/conversation queries, search, and data type conversions.
- **`contacts.go`** — Contact resolution: maps phone numbers and emails to human-readable names by reading the macOS AddressBook SQLite databases.

#### Connection Management

The package uses a **singleton connection pool** managed via `sync.Once`:

```
initDB() → sql.Open("sqlite3", "file:chat.db?mode=ro&_busy_timeout=3000&_journal_mode=WAL")
```

Key properties:
- **Read-only mode** (`mode=ro`) — the app never writes to `chat.db`.
- **WAL journal mode** — enables concurrent reads while Messages.app writes.
- **Busy timeout** (3s) — gracefully handles transient database locks.
- **Pool size:** 2 max open / 2 max idle connections with a 5-minute lifetime.
- `DB()` is the public accessor; `CloseDB()` is called from `main()` via `defer`.

#### Apple Timestamp Conversion

iMessage stores timestamps as nanoseconds since the Apple epoch (2001-01-01). The `AppleTimeToTime()` function handles the 978,307,200-second offset from Unix epoch and auto-detects nanosecond vs. second precision.

#### `attributedBody` Extraction

When the `text` column is empty (common for rich messages, edited messages, and certain iMessage effects), the `ExtractTextFromAttributedBody()` function parses the raw `NSAttributedString` binary plist blob using three progressively looser heuristics:

1. Split on `NSNumber`/`NSString`/`NSDictionary` markers and extract the text segment.
2. Look for text after the `streamtyped` marker.
3. Regex fallback — find the longest run of printable characters that aren't serialization artifacts.

#### Contact Resolution (`contacts.go`)

The `ContactResolver` lazily loads all contacts from every AddressBook source database found under `~/Library/Application Support/AddressBook/Sources/`. It builds two in-memory maps:

- `phoneToName` — normalized phone number → display name
- `emailToName` — lowercased email → display name

Phone number matching accounts for international format variations (e.g., `+15551234567`, `5551234567`, `15551234567` are all matched). The resolver is thread-safe (`sync.RWMutex`) and initialized once via `sync.Once`.

#### Key Query Functions

| Function | Description |
|----------|-------------|
| `GetConversations(limit)` | Retrieves recent conversations ordered by last message date, with participant info |
| `GetMessages(chatID, identifier, limit)` | Fetches messages for a specific chat, ordered oldest-first |
| `SearchMessages(query, limit)` | Full-text `LIKE` search across `text` and `attributedBody` columns |
| `GetUnreadCount()` | Counts messages where `is_read=0` and `is_from_me=0` |
| `GetContactByIdentifier(id)` | Looks up a contact/chat by phone number or email via the `handle` table |
| `ResolveSender(isFromMe, senderID)` | Returns "Me", a contact name, or "Unknown" |

### `internal/sender` — Message Sending

**Purpose:** Sends iMessages by invoking AppleScript via `osascript`.

**Mechanism:** The package shells out to `osascript` with AppleScript commands that control the Messages app. Each call has a 30-second `context.WithTimeout`.

**Send strategies (cascading fallback):**

1. **Direct buddy send** — `send "msg" to buddy "recipient" of targetService`
2. **Participant-based send** — `send "msg" to participant "recipient" of (1st chat whose participants contains ...)`
3. **New conversation send** — Creates a new participant on the iMessage service account

If all three strategies fail, the error is propagated to the caller.

**Additional functions:**
- `SendToGroup(chatName, message)` — sends to a named group chat.
- `CheckMessagesRunning()` — uses `System Events` to check if the Messages process is active.
- `StartMessagesApp()` — activates the Messages app.
- `escapeForAppleScript()` — escapes backslashes, quotes, newlines, and tabs for safe AppleScript string interpolation.

### `internal/watcher` — Real-Time Message Polling

**Purpose:** Monitors the iMessage database for changes and notifies subscribers via callbacks.

**Design pattern:** Observer pattern with a polling loop.

**How it works:**

1. On `Start()`, the watcher spawns a goroutine that runs `pollLoop()` — a ticker-based loop with a configurable interval (default 500ms).
2. Each tick performs two checks:
   - **New message detection:** Compares `MAX(ROWID) FROM message` against the last known value (stored atomically). If the max ID increased, it queries all new messages since the last ID and fires `MessageCallback`s.
   - **Conversation refresh:** Compares the mtime of `chat.db`, `chat.db-wal`, and `chat.db-shm` against the last known value. If any file changed, it re-fetches the conversation list and fires `ConversationCallback`s.
3. All callbacks are invoked in separate goroutines with `recover()` protection to prevent panics from crashing the watcher.

**Thread safety:** Callback slices are guarded by `sync.RWMutex`. The last-seen message ID and mtime are stored as `atomic.Int64` for lock-free reads in the hot path.

**Lifecycle:** `Stop()` closes the stop channel and calls `wg.Wait()` to ensure the poll goroutine exits cleanly.

### `internal/tui` — Terminal User Interface

**Purpose:** Full-screen interactive interface for browsing conversations and sending messages in real time.

**Framework:** [tview](https://github.com/rivo/tview) (built on [tcell](https://github.com/gdamore/tcell)).

**Layout:**

```
┌─────────────────┬─────────────────────────────────────┐
│  Conversations   │          Message View               │
│  (tview.List)    │        (tview.TextView)             │
│                  │                                     │
│                  │                                     │
│                  ├─────────────────────────────────────┤
│                  │  Input Field (tview.InputField)     │
├──────────────────┴─────────────────────────────────────┤
│  Status Bar (tview.TextView)                           │
└────────────────────────────────────────────────────────┘
```

**Key behaviors:**

- **Vim-style navigation:** `h/l` or arrow keys to switch panels; `j/k` to scroll messages; `g/G` for top/bottom; `i` to enter input mode; `q` to quit.
- **Single-instance enforcement:** Uses `flock()` on `~/.imessage-tui.lock` (with PID written for debugging) to prevent multiple TUI instances from running simultaneously.
- **Thread-safe UI updates:** All mutations from background goroutines go through `app.QueueUpdateDraw()` to avoid race conditions with tview's event loop.
- **Async message sending:** Sends are dispatched to a goroutine with an `atomic.Bool` guard (`sendingMessage`) to prevent double-sends. After a successful send, messages are refreshed after a 500ms delay.
- **Refresh with timeout:** Manual refresh (`r` key) fetches conversations and messages in parallel goroutines, each with a 5-second timeout to prevent indefinite hangs on a locked database.
- **Live updates:** The `watcher.MessageWatcher` fires callbacks that automatically update the conversation list and message view when new data arrives.
- **Debug mode:** `imessage tui --debug` enables structured logging to `/tmp/imessage-tui.log`, capturing input events, callback invocations, and timing — useful for diagnosing UI freeze issues.

## Data Flow

### Reading Messages

```
User runs `imessage read 1`
  → cli.cmdRead("1", 30)
    → database.GetConversations(100)     // resolve index to chat ID
    → database.GetMessages(chatID, "", 30)
      → SQL query on chat.db (read-only)
      → For each row: AppleTimeToTime(), ExtractTextFromAttributedBody(), ResolveSender()
    → Formatted output to stdout with ANSI colors
```

### Sending Messages

```
User runs `imessage send "+1234567890" "Hello"`
  → cli.cmdSend(recipient, message, skipConfirm)
    → [Optional confirmation prompt]
    → sender.SendMessage(recipient, message)
      → osascript: tell Messages to send via buddy
      → [On failure] osascript: send via participant of chat
      → [On failure] osascript: send via new participant on iMessage service
    → Success/error output
```

### TUI Live Updates

```
tui.run()
  → loadInitialData()           // sync load before app.Run()
  → watcher.Start()             // begins 500ms poll loop
  → app.Run()                   // tview event loop
  
Poll tick:
  → watcher.poll()
    → SELECT MAX(ROWID) FROM message
    → If new: GetNewMessages(sinceID) → MessageCallback → tui.onNewMessages()
    → If mtime changed: GetConversations() → ConversationCallback → tui.onConversationsUpdated()
    → Callbacks call app.QueueUpdateDraw() to safely update UI
```

## External Dependencies

| Dependency | Version | Purpose |
|------------|---------|---------|
| `github.com/spf13/cobra` | v1.8.0 | CLI command framework |
| `github.com/mattn/go-sqlite3` | v1.14.22 | CGo SQLite3 driver for reading `chat.db` and AddressBook |
| `github.com/rivo/tview` | v0.0.0-20240101 | Terminal UI framework |
| `github.com/gdamore/tcell/v2` | v2.7.0 | Terminal cell library (tview dependency) |

## System Requirements & Permissions

- **macOS only** — depends on `~/Library/Messages/chat.db`, macOS AddressBook databases, and `osascript`.
- **Full Disk Access** — the terminal emulator must have Full Disk Access in System Preferences to read `chat.db`.
- **Messages app** — must be configured and signed in for both reading and sending.
- **CGo** — required by `go-sqlite3`; the build needs a C compiler.

## Concurrency Model

The application uses several concurrency patterns:

1. **Singleton initialization** — `sync.Once` for the database connection pool and contact resolver.
2. **Atomic flags** — `atomic.Bool` for send-in-progress and refresh-in-progress guards; `atomic.Int64` for last message ID and last mtime in the watcher.
3. **Mutex-protected shared state** — `sync.RWMutex` in the TUI for the conversation/message slices, and in the contact resolver for the name maps.
4. **Channel-based coordination** — The watcher uses a `stopCh` channel for clean shutdown; the TUI refresh uses channels with `select` timeouts.
5. **`QueueUpdateDraw`** — All background goroutines funnel UI mutations through tview's thread-safe update queue.

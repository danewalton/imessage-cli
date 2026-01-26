# iMessage CLI

A command-line interface for reading and responding to iMessages on macOS. Perfect for SSH sessions when you want to check and reply to messages without leaving the terminal.

## Features

- �️ **Interactive TUI** - Full-screen interface with real-time message updates
- 📋 **List conversations** - View recent chats with timestamps
- 📖 **Read messages** - View message history for any conversation
- 📤 **Send messages** - Reply to contacts directly from the CLI
- 💬 **Interactive chat mode** - Real-time conversation interface
- 🔍 **Search messages** - Find messages containing specific text
- 📊 **Status check** - Verify setup and view statistics
- 🔄 **Live updates** - Database watcher for real-time notifications

## Requirements

- macOS (tested on Monterey, Ventura, Sonoma, and Sequoia)
- Python 3.8 or later
- Messages app configured with an iCloud account
- **Full Disk Access** permission for Terminal (or your SSH client)

## Installation

### Quick Start (No Installation)

```bash
# Clone or download the repository
git clone https://github.com/yourusername/imessage-cli.git
cd imessage-cli

# Make the script executable
chmod +x imessage

# Run directly
./imessage list
```

### Install as Package

```bash
# Install in development mode
pip install -e .

# Or install directly
pip install .

# Now you can run from anywhere
imessage list
```

## Usage

### Interactive TUI (Recommended for SSH)

The TUI provides a full-screen, real-time interface that automatically updates when new messages arrive:

```bash
# Launch the TUI
imessage tui

# Aliases
imessage ui
imessage watch
```

**TUI Keyboard Controls:**

| Key | Action |
|-----|--------|
| `↑/↓` or `j/k` | Navigate conversations/scroll messages |
| `←/→` or `h/l` | Switch between panels |
| `Tab` | Switch focus between panels |
| `Enter` | Select conversation |
| `i` | Enter input mode to type a message |
| `Escape` | Exit input mode |
| `r` | Refresh messages |
| `g/G` | Go to top/bottom of messages |
| `PgUp/PgDn` | Scroll messages by page |
| `q` | Quit |

### List Conversations

```bash
# Show recent conversations
imessage list

# Show more conversations
imessage list -n 50

# Short aliases work too
imessage ls
imessage l
```

### Read Messages

```bash
# Read messages from conversation #1 (from the list)
imessage read 1

# Read messages from a specific phone number
imessage read "+1234567890"

# Read more messages
imessage read 1 -n 100

# Aliases
imessage r 1
imessage view 1
```

### Send Messages

```bash
# Send a message (with confirmation prompt)
imessage send "+1234567890" "Hello from the terminal!"

# Skip confirmation
imessage send "+1234567890" "Quick message" -y

# Alias
imessage s "+1234567890" "Hi there"
```

### Interactive Chat Mode

```bash
# Start interactive chat with conversation #1
imessage chat 1

# Or with a phone number
imessage chat "+1234567890"

# In chat mode:
#   - Type messages and press Enter to send
#   - Type 'r' or 'refresh' to reload messages
#   - Type 'quit' or Ctrl+C to exit
```

### Search Messages

```bash
# Search for messages containing text
imessage search "meeting tomorrow"

# Limit results
imessage search "lunch" -n 10

# Aliases
imessage find "project"
imessage grep "hello"
```

### Check Status

```bash
# View status and statistics
imessage status
```

## Permissions Setup

### Grant Full Disk Access

For the CLI to read the Messages database, you need to grant Full Disk Access:

1. Open **System Preferences** (or **System Settings** on newer macOS)
2. Go to **Privacy & Security** → **Full Disk Access**
3. Click the lock to make changes
4. Add **Terminal** (or iTerm, or your SSH client)
5. Restart Terminal

### For SSH Access

If you're SSHing into your Mac, you'll need to grant Full Disk Access to the SSH daemon:

1. Add `/usr/sbin/sshd` to Full Disk Access
2. Alternatively, if using a custom SSH server, add that instead

## Troubleshooting

### "iMessage database not found"

- Make sure Messages is configured and you've sent/received at least one message
- Check that Full Disk Access is granted

### "Failed to send message"

- Ensure Messages app is running (the CLI will try to start it)
- Verify the recipient is a valid phone number or email
- Check that your iCloud account is signed in to Messages

### Messages not showing

- The database might take a moment to sync
- Try running `imessage status` to verify database access

### Permission denied errors

- Double-check Full Disk Access permissions
- Try running from a fresh Terminal session after granting permissions

## How It Works

- **Reading**: The CLI reads directly from the Messages SQLite database located at `~/Library/Messages/chat.db`
- **Sending**: Messages are sent using AppleScript to control the Messages app

## Privacy Note

This tool only accesses your local Messages database. No data is sent to external servers. All message sending goes through Apple's official Messages app.

## License

MIT License - see LICENSE file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

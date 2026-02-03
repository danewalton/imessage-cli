"""TUI (Text User Interface) for iMessage CLI using curses.

Upgrades:
- Multi-line input box with basic editing, history (↑/↓), and common shortcuts:
  Ctrl-A (home), Ctrl-E (end), Ctrl-W (delete previous word), Ctrl-K (kill to end),
  Ctrl-U (clear), Enter to send (preserve text on failure).
- Help overlay toggled with '?'.
- Safer color initialization with curses.has_colors() checks and fallbacks.
- Minimum terminal size check and graceful layout when narrow.
- Uses wcwidth.wcswidth when available for accurate unicode width calculations,
  with a fallback to len() if wcwidth is not installed.
"""

import curses
import threading
import queue
import textwrap
import logging
from datetime import datetime
from typing import List, Optional, Tuple

from .watcher import MessageWatcher, Message, Conversation
from .sender import send_message

# Try to use wcwidth for accurate width calculation (emoji, CJK, etc.)
try:
    from wcwidth import wcswidth
except Exception:

    def wcswidth(s: str) -> int:
        return len(s or "")


# --- Logging for send errors / debug (writes to a log file in repo directory) ---
logger = logging.getLogger("imessage_cli.tui")
if not logger.handlers:
    fh = logging.FileHandler("imessage_tui.log")
    fh.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s"))
    logger.addHandler(fh)
logger.setLevel(logging.INFO)


# Key constants for clarity
CTRL_A = 1
CTRL_E = 5
CTRL_U = 21
CTRL_W = 23
CTRL_K = 11
ENTER_KEYS = (10, 13)


def safe_wcswidth(s: str) -> int:
    """Wrapper around wcswidth with defensive behavior."""
    try:
        return max(0, wcswidth(s or ""))
    except Exception:
        return len(s or "")


class MultiLineInputBox:
    """A multi-line input box with a tiny edit buffer and history."""

    def __init__(self, window, y: int, x: int, width: int, max_lines: int = 3):
        self.window = window
        self.y = y
        self.x = x
        self.width = width
        self.max_lines = max_lines

        self.lines: List[str] = [""]
        self.cursor_row = 0
        self.cursor_col = 0

        # History support
        self.history: List[str] = []
        self.history_idx: Optional[int] = None

    # --- Buffer helpers ---
    def get_text(self) -> str:
        return "\n".join(self.lines).rstrip("\n")

    def set_text(self, text: str):
        self.lines = text.split("\n") or [""]
        if not self.lines:
            self.lines = [""]
        self.cursor_row = len(self.lines) - 1
        self.cursor_col = len(self.lines[self.cursor_row])

    def clear(self):
        self.lines = [""]
        self.cursor_row = 0
        self.cursor_col = 0
        self.history_idx = None

    def push_history(self, text: str):
        if text.strip():
            # Avoid duplicates of consecutive identical entries
            if not self.history or self.history[-1] != text:
                self.history.append(text)
        self.history_idx = None

    def recall_history(self, direction: int) -> Optional[str]:
        """direction: -1 for up (older), 1 for down (newer)."""
        if not self.history:
            return None
        if self.history_idx is None:
            if direction == -1:
                self.history_idx = len(self.history) - 1
            else:
                return None
        else:
            self.history_idx += direction
            if self.history_idx < 0:
                self.history_idx = 0
            elif self.history_idx >= len(self.history):
                self.history_idx = len(self.history) - 1

        return self.history[self.history_idx]

    # --- Editing primitives ---
    def _move_left(self):
        if self.cursor_col > 0:
            self.cursor_col -= 1
        elif self.cursor_row > 0:
            self.cursor_row -= 1
            self.cursor_col = len(self.lines[self.cursor_row])

    def _move_right(self):
        if self.cursor_col < len(self.lines[self.cursor_row]):
            self.cursor_col += 1
        elif self.cursor_row < len(self.lines) - 1:
            self.cursor_row += 1
            self.cursor_col = 0

    def _move_up(self):
        if self.cursor_row > 0:
            self.cursor_row -= 1
            self.cursor_col = min(self.cursor_col, len(self.lines[self.cursor_row]))

    def _move_down(self):
        if self.cursor_row < len(self.lines) - 1:
            self.cursor_row += 1
            self.cursor_col = min(self.cursor_col, len(self.lines[self.cursor_row]))

    def _delete_backwards(self):
        if self.cursor_col > 0:
            line = self.lines[self.cursor_row]
            self.lines[self.cursor_row] = (
                line[: self.cursor_col - 1] + line[self.cursor_col :]
            )
            self.cursor_col -= 1
        elif self.cursor_row > 0:
            # Join with previous line
            prev = self.lines[self.cursor_row - 1]
            cur = self.lines.pop(self.cursor_row)
            self.cursor_row -= 1
            self.cursor_col = len(prev)
            self.lines[self.cursor_row] = prev + cur

    def _delete_word_backwards(self):
        """Delete previous word (Ctrl-W)."""
        line = self.lines[self.cursor_row]
        if self.cursor_col == 0 and self.cursor_row > 0:
            # Merge with previous line
            prev = self.lines[self.cursor_row - 1]
            self.lines[self.cursor_row - 1] = prev + line
            self.lines.pop(self.cursor_row)
            self.cursor_row -= 1
            self.cursor_col = len(prev)
            return

        # Find previous word boundary
        i = self.cursor_col
        # Skip whitespace
        while i > 0 and line[i - 1].isspace():
            i -= 1
        # Skip non-whitespace
        while i > 0 and not line[i - 1].isspace():
            i -= 1
        self.lines[self.cursor_row] = line[:i] + line[self.cursor_col :]
        self.cursor_col = i

    def _kill_to_end(self):
        """Kill from cursor to end of line (Ctrl-K)."""
        line = self.lines[self.cursor_row]
        self.lines[self.cursor_row] = line[: self.cursor_col]

    # --- Key handling ---
    def handle_key(self, key: int) -> Optional[str]:
        """Handle a keypress. Return the text to send when Enter pressed.

        Important: This function does NOT clear the buffer on Enter; the caller
        should call clear() only when a send succeeds.
        """
        # Navigation & editing
        if key in (curses.KEY_BACKSPACE, 127, 8):
            self._delete_backwards()

        elif key == curses.KEY_DC:  # Delete
            line = self.lines[self.cursor_row]
            if self.cursor_col < len(line):
                self.lines[self.cursor_row] = (
                    line[: self.cursor_col] + line[self.cursor_col + 1 :]
                )
            elif self.cursor_row < len(self.lines) - 1:
                # join with next line
                nxt = self.lines.pop(self.cursor_row + 1)
                self.lines[self.cursor_row] = line + nxt

        elif key == curses.KEY_LEFT:
            self._move_left()

        elif key == curses.KEY_RIGHT:
            self._move_right()

        elif key == curses.KEY_UP:
            # If multi-line input and cursor can move up, do it; otherwise recall history
            if self.cursor_row > 0:
                self._move_up()
            else:
                recalled = self.recall_history(-1)
                if recalled is not None:
                    self.set_text(recalled)

        elif key == curses.KEY_DOWN:
            if self.cursor_row < len(self.lines) - 1:
                self._move_down()
            else:
                recalled = self.recall_history(1)
                if recalled is not None:
                    self.set_text(recalled)

        elif key == curses.KEY_HOME or key == CTRL_A:
            self.cursor_col = 0

        elif key == curses.KEY_END or key == CTRL_E:
            self.cursor_col = len(self.lines[self.cursor_row])

        elif key == CTRL_U:
            self.clear()

        elif key == CTRL_W:
            self._delete_word_backwards()

        elif key == CTRL_K:
            self._kill_to_end()

        elif key in ENTER_KEYS:
            # Join lines into text and return it for sending
            text = self.get_text()
            # Leave buffer intact; caller will clear only on success
            return text

        elif key == curses.KEY_ENTER:
            text = self.get_text()
            return text

        elif key == 9:  # Tab - insert tab as spaces
            self._insert_text("    ")

        elif 0 <= key < 256:
            ch = chr(key)
            # Printable? (control chars should be guarded)
            if ch.isprintable():
                self._insert_text(ch)

        return None

    def _insert_text(self, s: str):
        line = self.lines[self.cursor_row]
        self.lines[self.cursor_row] = (
            line[: self.cursor_col] + s + line[self.cursor_col :]
        )
        # Move cursor right by the inserted string's visual width (approx by characters)
        # For simplicity, move by characters
        self.cursor_col += len(s)

    # --- Rendering ---
    def draw(self, prompt: str = "Send: "):
        """Draw a small multi-line input box at self.window positioned at y,x.

        The window is expected to be tall enough for self.max_lines lines (1..max_lines).
        """
        try:
            # Clear the input window area
            self.window.erase()

            # Determine how many lines to render (max self.max_lines)
            lines_to_show = self.lines[-self.max_lines :]
            # Starting row (top of input area) is 0 inside the input window
            for i, line in enumerate(lines_to_show):
                # Truncate to available width using visual width approximation
                available = max(1, self.width - len(prompt) - 1)
                visible = self._trim_to_width(line, available)
                self.window.addstr(i, 0, prompt if i == 0 else " " * len(prompt))
                self.window.addstr(i, len(prompt), visible)

            # Position the terminal cursor
            # Map cursor_row/col to visible positions (considering trimming of lines)
            visible_row = (
                len(lines_to_show) - 1 - (len(self.lines) - 1 - self.cursor_row)
            )
            if visible_row < 0:
                visible_row = 0
                visible_col = 0
            else:
                # compute visual column up to cursor_col
                cursor_text = self.lines[self.cursor_row][: self.cursor_col]
                visible_col = self._trimmed_col(cursor_text, available)
            self.window.move(visible_row, len(prompt) + visible_col)
        except curses.error:
            # If drawing fails due to small terminal, ignore
            pass

    def _trim_to_width(self, s: str, width: int) -> str:
        """Trim string s so its wcswidth <= width."""
        if safe_wcswidth(s) <= width:
            return s
        # naive slice by characters until width fits
        out = ""
        cur = 0
        for ch in s:
            w = safe_wcswidth(ch)
            if cur + w > width:
                break
            out += ch
            cur += w
        return out

    def _trimmed_col(self, s: str, width: int) -> int:
        """Return visual column of s, trimmed to width if necessary."""
        total = safe_wcswidth(s)
        if total <= width:
            return total
        # if trimmed, return width
        return width


class MessagesTUI:
    """Main TUI application for iMessage."""

    # Color pair IDs
    COLOR_NORMAL = 1
    COLOR_SELECTED = 2
    COLOR_SENT = 3
    COLOR_RECEIVED = 4
    COLOR_HEADER = 5
    COLOR_STATUS = 6
    COLOR_UNREAD = 7
    COLOR_INPUT = 8
    COLOR_ERROR = 9

    MIN_HEIGHT = 10
    MIN_WIDTH = 50

    def __init__(self, stdscr):
        self.stdscr = stdscr
        self.watcher = MessageWatcher(poll_interval=0.5)

        # State
        self.conversations: List[Conversation] = []
        self.messages: List[Message] = []
        self.selected_conv_idx = 0
        self.selected_chat_id: Optional[int] = None
        self.message_scroll = 0
        self.conv_scroll = 0

        # UI state
        self.focus = "conversations"  # 'conversations', 'messages', 'input'
        self.input_box: Optional[MultiLineInputBox] = None
        self.status_message = ""
        self.error_message = ""
        self.show_help = False

        # Threading
        self.update_queue = queue.Queue()
        self.running = True

        # Setup
        self._setup_colors()
        self._setup_windows()
        self._setup_watcher()

    def _setup_colors(self):
        """Initialize color pairs with safety checks."""
        if not curses.has_colors():
            # Nothing to setup; use attributes only
            return

        try:
            curses.start_color()
            curses.use_default_colors()
            # Use color pairs but guard for terminals with small color sets
            curses.init_pair(self.COLOR_NORMAL, -1, -1)
            # selection: reverse or black-on-white if bright colors available
            curses.init_pair(self.COLOR_SELECTED, curses.COLOR_WHITE, curses.COLOR_BLUE)
            curses.init_pair(self.COLOR_SENT, curses.COLOR_GREEN, -1)
            curses.init_pair(self.COLOR_RECEIVED, curses.COLOR_CYAN, -1)
            curses.init_pair(self.COLOR_HEADER, curses.COLOR_BLACK, curses.COLOR_WHITE)
            curses.init_pair(self.COLOR_STATUS, curses.COLOR_BLACK, curses.COLOR_GREEN)
            curses.init_pair(self.COLOR_UNREAD, curses.COLOR_YELLOW, -1)
            curses.init_pair(self.COLOR_INPUT, curses.COLOR_WHITE, -1)
            curses.init_pair(self.COLOR_ERROR, curses.COLOR_RED, -1)
        except curses.error:
            # Some terminals may not support all color initializations
            pass

    def _setup_windows(self):
        """Create the window layout. Input area uses multiple lines."""
        self.height, self.width = self.stdscr.getmaxyx()

        # Decide input area height
        self.input_height = 3  # render up to 3 lines of input
        # Reserve two rows for status + separation
        reserved = self.input_height + 1
        self.conv_width = min(35, max(20, self.width // 3))
        self.msg_width = max(20, self.width - self.conv_width)
        self.msg_height = max(5, self.height - reserved)

        # If terminal is too narrow, collapse conversations pane
        if self.width < 80:
            self.conv_width = min(20, max(10, self.width // 4))
            # If extremely narrow, hide conv pane entirely
            if self.width < 60:
                self.conv_width = 0
                self.msg_width = self.width
            else:
                self.msg_width = self.width - self.conv_width

        # Create windows; guard for zero width conv_win
        if self.conv_width > 0:
            self.conv_win = curses.newwin(self.msg_height, self.conv_width, 0, 0)
        else:
            self.conv_win = None
        self.msg_win = curses.newwin(
            self.msg_height, self.msg_width, 0, self.conv_width
        )
        self.status_win = curses.newwin(1, self.width, self.msg_height, 0)
        self.input_win = curses.newwin(
            self.input_height, self.width, self.msg_height + 1, 0
        )

        # Enable scrolling where applicable
        if self.conv_win:
            self.conv_win.scrollok(True)
            self.conv_win.keypad(True)
        self.msg_win.scrollok(True)

        # Input box instance
        self.input_box = MultiLineInputBox(
            self.input_win, 0, 0, self.width, max_lines=self.input_height
        )

        # Enable keypad on stdscr and windows
        self.stdscr.keypad(True)
        if self.conv_win:
            self.conv_win.keypad(True)
        self.msg_win.keypad(True)
        self.input_win.keypad(True)

    def _setup_watcher(self):
        """Setup the message watcher callbacks."""
        self.watcher.on_new_messages(self._on_new_messages)
        self.watcher.on_conversations_updated(self._on_conversations_updated)
        self.watcher.on_error(self._on_error)

    def _on_new_messages(self, messages: List[Message]):
        """Callback when new messages arrive."""
        self.update_queue.put(("new_messages", messages))

    def _on_conversations_updated(self, conversations: List[Conversation]):
        """Callback when conversations are updated."""
        self.update_queue.put(("conversations", conversations))

    def _on_error(self, error: Exception):
        """Callback when an error occurs."""
        self.update_queue.put(("error", str(error)))

    def _process_updates(self):
        """Process any pending updates from the watcher."""
        try:
            while True:
                update_type, data = self.update_queue.get_nowait()

                if update_type == "conversations":
                    self.conversations = data
                    self._draw_conversations()

                elif update_type == "new_messages":
                    # Check if any messages are for the current chat
                    for msg in data:
                        if msg.chat_id == self.selected_chat_id:
                            self.messages.append(msg)
                            # Auto-scroll to bottom
                            self.message_scroll = max(
                                0, len(self.messages) - self._visible_message_lines()
                            )
                    self._draw_messages()
                    self._draw_conversations()  # Update unread counts

                    # Flash notification
                    if data and not data[-1].is_from_me:
                        self.status_message = f"New message from {data[-1].sender}"
                        try:
                            curses.flash()
                        except Exception:
                            pass

                elif update_type == "error":
                    self.error_message = str(data)

        except queue.Empty:
            pass

    def _visible_message_lines(self) -> int:
        """Calculate how many message lines are visible."""
        return max(1, self.msg_height - 2)  # Account for header/border

    def _draw_header(self, win, title: str, color_pair: int):
        """Draw a header bar."""
        if win is None:
            return
        try:
            if curses.has_colors():
                win.attron(curses.color_pair(color_pair))
            win.addstr(0, 0, title.center(win.getmaxyx()[1])[: win.getmaxyx()[1] - 1])
            if curses.has_colors():
                win.attroff(curses.color_pair(color_pair))
        except curses.error:
            pass

    def _draw_conversations(self):
        """Draw the conversations list."""
        if self.conv_win is None:
            return
        self.conv_win.erase()

        # Header
        header = " Conversations "
        self._draw_header(self.conv_win, header, self.COLOR_HEADER)

        if not self.conversations:
            try:
                self.conv_win.addstr(2, 1, "No conversations")
            except curses.error:
                pass
            self.conv_win.refresh()
            return

        # Calculate visible range
        visible_height = self.msg_height - 2

        # Adjust scroll if needed
        if self.selected_conv_idx < self.conv_scroll:
            self.conv_scroll = self.selected_conv_idx
        elif self.selected_conv_idx >= self.conv_scroll + visible_height:
            self.conv_scroll = self.selected_conv_idx - visible_height + 1

        # Draw conversations
        for i, conv in enumerate(
            self.conversations[self.conv_scroll : self.conv_scroll + visible_height]
        ):
            idx = i + self.conv_scroll
            y = i + 1

            if y >= self.msg_height - 1:
                break

            # Truncate name using visual width approximation
            name = conv.display_name
            # Simple truncation by characters to fit
            max_name_len = max(1, self.conv_width - 6)
            if len(name) > max_name_len:
                name = name[: max_name_len - 1] + "…"

            # Format time
            time_str = (
                self._format_time(conv.last_message_date)
                if conv.last_message_date
                else ""
            )

            # Build display line
            line = f" {name}"

            try:
                if idx == self.selected_conv_idx:
                    if curses.has_colors():
                        self.conv_win.attron(curses.color_pair(self.COLOR_SELECTED))
                    self.conv_win.addstr(
                        y, 0, line.ljust(self.conv_width - 1)[: self.conv_width - 1]
                    )
                    if curses.has_colors():
                        self.conv_win.attroff(curses.color_pair(self.COLOR_SELECTED))
                else:
                    if conv.unread_count > 0:
                        if curses.has_colors():
                            self.conv_win.attron(
                                curses.color_pair(self.COLOR_UNREAD) | curses.A_BOLD
                            )
                        unread_indicator = f"({conv.unread_count})"
                        self.conv_win.addstr(
                            y, 0, f" {unread_indicator} {name}"[: self.conv_width - 1]
                        )
                        if curses.has_colors():
                            self.conv_win.attroff(
                                curses.color_pair(self.COLOR_UNREAD) | curses.A_BOLD
                            )
                    else:
                        self.conv_win.addstr(y, 0, line[: self.conv_width - 1])

                # Add time on the right
                if time_str:
                    time_x = self.conv_width - len(time_str) - 2
                    if time_x > len(name) + 2:
                        self.conv_win.addstr(y, time_x, time_str, curses.A_DIM)

            except curses.error:
                pass

        # Draw border
        try:
            for y in range(self.msg_height):
                self.conv_win.addch(y, self.conv_width - 1, curses.ACS_VLINE)
        except curses.error:
            pass

        self.conv_win.refresh()

    def _draw_messages(self):
        """Draw the messages panel."""
        self.msg_win.erase()

        # Header
        if self.selected_chat_id and self.conversations:
            conv = next(
                (c for c in self.conversations if c.chat_id == self.selected_chat_id),
                None,
            )
            header = f" {conv.display_name} " if conv else " Messages "
        else:
            header = " Messages "

        self._draw_header(self.msg_win, header, self.COLOR_HEADER)

        if not self.messages:
            try:
                self.msg_win.addstr(
                    2,
                    1,
                    (
                        "Select a conversation"
                        if not self.selected_chat_id
                        else "No messages"
                    ),
                )
            except curses.error:
                pass
            self.msg_win.refresh()
            return

        # Calculate layout
        visible_height = self.msg_height - 2
        msg_display_width = max(10, self.msg_width - 4)

        # Pre-render all message lines (simple wrap using textwrap; could be optimized)
        all_lines: List[Tuple[str, int, bool]] = []  # (text, color, is_from_me)

        for msg in self.messages:
            time_str = self._format_time(msg.date) if msg.date else ""

            if msg.is_from_me:
                prefix = f"[{time_str}] Me: "
                color = self.COLOR_SENT
            else:
                sender = msg.sender[:15] if len(msg.sender) > 15 else msg.sender
                prefix = f"[{time_str}] {sender}: "
                color = self.COLOR_RECEIVED

            # Word wrap the message (visual wrapping isn't perfect but adequate)
            text = msg.text or ""
            text_lines = text.split("\n")

            first_line = True
            for text_line in text_lines:
                if first_line:
                    wrapped = textwrap.wrap(prefix + text_line, msg_display_width) or [
                        prefix
                    ]
                    first_line = False
                else:
                    indent = " " * min(len(prefix), 20)
                    wrapped = textwrap.wrap(indent + text_line, msg_display_width) or [
                        indent
                    ]

                for line in wrapped:
                    all_lines.append((line, color, msg.is_from_me))

        # Adjust scroll
        total_lines = len(all_lines)
        max_scroll = max(0, total_lines - visible_height)
        self.message_scroll = min(self.message_scroll, max_scroll)

        # Draw visible lines
        y = 1
        for line_text, color, is_from_me in all_lines[
            self.message_scroll : self.message_scroll + visible_height
        ]:
            if y >= self.msg_height - 1:
                break

            try:
                if curses.has_colors():
                    self.msg_win.attron(curses.color_pair(color))
                self.msg_win.addstr(y, 1, line_text[: self.msg_width - 2])
                if curses.has_colors():
                    self.msg_win.attroff(curses.color_pair(color))
            except curses.error:
                pass

            y += 1

        # Scroll indicator
        if total_lines > visible_height:
            scroll_pct = self.message_scroll / max_scroll if max_scroll > 0 else 0
            indicator_y = int(1 + scroll_pct * (visible_height - 1))
            try:
                self.msg_win.addch(indicator_y, self.msg_width - 1, curses.ACS_DIAMOND)
            except curses.error:
                pass

        self.msg_win.refresh()

    def _draw_status(self):
        """Draw the status bar."""
        self.status_win.erase()

        # Build status line
        if self.error_message:
            status = f" Error: {self.error_message}"
            if curses.has_colors():
                self.status_win.attron(curses.color_pair(self.COLOR_ERROR))
        elif self.status_message:
            status = f" {self.status_message}"
            if curses.has_colors():
                self.status_win.attron(curses.color_pair(self.COLOR_STATUS))
        else:
            focus_indicator = {
                "conversations": "[CONV]",
                "messages": "[MSG]",
                "input": "[INPUT]",
            }.get(self.focus, "")

            help_text = "↑↓:Nav  Enter:Send  i:Input  Tab:Switch  ?:Help  q:Quit"
            status = f" {focus_indicator} {help_text}"
            if curses.has_colors():
                self.status_win.attron(curses.color_pair(self.COLOR_STATUS))

        try:
            self.status_win.addstr(0, 0, status.ljust(self.width)[: self.width - 1])
        except curses.error:
            pass

        if curses.has_colors():
            try:
                self.status_win.attroff(curses.color_pair(self.COLOR_STATUS))
                self.status_win.attroff(curses.color_pair(self.COLOR_ERROR))
            except Exception:
                pass

        self.status_win.refresh()

        # Clear ephemeral messages
        self.status_message = ""
        self.error_message = ""

    def _draw_input(self):
        """Draw the input box."""
        self.input_win.erase()

        if self.focus == "input":
            prompt = "Send: "
            self.input_box.draw(prompt)
            try:
                curses.curs_set(1)  # Show cursor
            except curses.error:
                pass
        else:
            try:
                curses.curs_set(0)  # Hide cursor
            except curses.error:
                pass
            try:
                hint = "Press 'i' to type a message, 'q' to quit"
                self.input_win.addstr(0, 0, hint, curses.A_DIM)
            except curses.error:
                pass

        self.input_win.refresh()

    def _draw_help_overlay(self):
        """Draw a centered help modal listing the key bindings."""
        help_lines = [
            "iMessage CLI - Help",
            "",
            "Navigation:",
            "  ↑/↓ or j/k      Navigate conversations / scroll messages",
            "  ←/→ or h/l      Switch between panels",
            "  Tab             Switch focus between panels",
            "",
            "Input (when focused):",
            "  Enter           Send message",
            "  Ctrl-A / Home   Move to start of line",
            "  Ctrl-E / End    Move to end of line",
            "  Ctrl-W          Delete previous word",
            "  Ctrl-K          Kill to end of line",
            "  Ctrl-U          Clear input",
            "  ↑/↓             Recall input history",
            "",
            "Other:",
            "  ?               Toggle this help",
            "  r               Refresh messages",
            "  q               Quit",
        ]

        # Determine modal size
        w = max(40, min(self.width - 4, 80))
        h = min(len(help_lines) + 4, self.height - 4)
        start_y = max(0, (self.height - h) // 2)
        start_x = max(0, (self.width - w) // 2)

        try:
            modal = curses.newwin(h, w, start_y, start_x)
            modal.box()
            for i, ln in enumerate(help_lines[: h - 2]):
                try:
                    modal.addstr(1 + i, 1, ln[: w - 2])
                except curses.error:
                    pass
            modal.refresh()
        except curses.error:
            pass

    def _format_time(self, dt: Optional[datetime]) -> str:
        """Format datetime for display."""
        if dt is None:
            return ""

        now = datetime.now()
        diff = now - dt

        if diff.days == 0:
            return dt.strftime("%H:%M")
        elif diff.days == 1:
            return "Yesterday"
        elif diff.days < 7:
            return dt.strftime("%a")
        else:
            return dt.strftime("%m/%d")

    def _select_conversation(self, idx: int):
        """Select a conversation and load its messages."""
        if 0 <= idx < len(self.conversations):
            self.selected_conv_idx = idx
            conv = self.conversations[idx]
            self.selected_chat_id = conv.chat_id

            # Load messages
            self.messages = self.watcher.get_messages(conv.chat_id)
            self.message_scroll = max(
                0, len(self.messages) - self._visible_message_lines()
            )

            self._draw_conversations()
            self._draw_messages()

    def _send_current_message(self, text: str) -> bool:
        """Send the current message.

        Returns True on success, False otherwise. Does not mutate the input_box buffer.
        """
        if not text.strip():
            return False

        if not self.selected_chat_id or not self.conversations:
            self.error_message = "No conversation selected"
            return False

        conv = next(
            (c for c in self.conversations if c.chat_id == self.selected_chat_id), None
        )
        if not conv:
            self.error_message = "Conversation not found"
            return False

        try:
            success = bool(send_message(conv.chat_identifier, text))
            if success:
                self.status_message = "Message sent!"
                logger.info("Sent message to %s: %s", conv.display_name, text[:120])
                return True
            else:
                self.error_message = "Failed to send message"
                logger.warning("Send failed for %s: %s", conv.display_name, text[:120])
                return False
        except Exception as e:
            self.error_message = f"Send error: {str(e)[:80]}"
            logger.exception("Send exception for %s", conv.display_name)
            return False

    def _handle_resize(self):
        """Handle terminal resize."""
        self.height, self.width = self.stdscr.getmaxyx()
        self._setup_windows()
        self.draw_all()

    def draw_all(self):
        """Redraw all windows."""
        self._draw_conversations()
        self._draw_messages()
        self._draw_status()
        self._draw_input()
        if self.show_help:
            self._draw_help_overlay()

    def run(self):
        """Main event loop."""
        # Initial load
        self.conversations = self.watcher.get_conversations()
        if self.conversations:
            self._select_conversation(0)

        # Start watcher
        self.watcher.start()

        # Set non-blocking input
        self.stdscr.nodelay(True)
        self.stdscr.timeout(100)  # 100ms timeout

        # Check terminal minimum size
        if self.height < self.MIN_HEIGHT or self.width < self.MIN_WIDTH:
            # Inform user and wait for resize or quit
            self.stdscr.erase()
            try:
                self.stdscr.addstr(
                    0,
                    0,
                    f"Terminal too small ({self.width}x{self.height}). Resize to at least {self.MIN_WIDTH}x{self.MIN_HEIGHT} or press q to quit.",
                )
            except curses.error:
                pass
            self.stdscr.refresh()

        self.draw_all()

        try:
            while self.running:
                # Process any updates from watcher
                self._process_updates()

                # Get input
                try:
                    key = self.stdscr.getch()
                except curses.error:
                    key = -1

                if key == -1:
                    continue

                if key == curses.KEY_RESIZE:
                    self._handle_resize()
                    continue

                # Global keys (help / quit) that work regardless of focus
                if key == ord("?"):
                    self.show_help = not self.show_help
                    if self.show_help:
                        self._draw_help_overlay()
                    else:
                        self.draw_all()
                    continue

                if key == ord("q") or key == ord("Q"):
                    self.running = False
                    continue

                # If help overlay is showing, ignore other keys
                if self.show_help:
                    continue

                # Handle input based on focus
                if self.focus == "input":
                    if key == 27:  # Escape
                        self.focus = "messages"
                        self._draw_status()
                        self._draw_input()
                    else:
                        result = self.input_box.handle_key(key)
                        if result is not None:
                            # Attempt to send; only clear input on success
                            success = self._send_current_message(result)
                            if success:
                                # store into history and clear buffer
                                self.input_box.push_history(result)
                                self.input_box.clear()
                            else:
                                # keep text in input buffer and show error in status
                                pass
                            self.focus = "messages"
                            self._draw_status()
                        self._draw_input()

                elif self.focus == "conversations":
                    if key == curses.KEY_UP or key == ord("k"):
                        if self.selected_conv_idx > 0:
                            self._select_conversation(self.selected_conv_idx - 1)
                    elif key == curses.KEY_DOWN or key == ord("j"):
                        if self.selected_conv_idx < len(self.conversations) - 1:
                            self._select_conversation(self.selected_conv_idx + 1)
                    elif (
                        key in ENTER_KEYS or key == curses.KEY_RIGHT or key == ord("l")
                    ):
                        self.focus = "messages"
                        self._draw_status()
                        self._draw_conversations()
                    elif key == ord("i"):
                        self.focus = "input"
                        self._draw_status()
                        self._draw_input()
                    elif key == 9:  # Tab
                        self.focus = "messages"
                        self._draw_status()
                    elif key == ord("r") or key == ord("R"):
                        # Refresh
                        self.conversations = self.watcher.get_conversations()
                        if self.selected_chat_id:
                            self.messages = self.watcher.get_messages(
                                self.selected_chat_id
                            )
                        self.draw_all()
                        self.status_message = "Refreshed"
                        self._draw_status()

                elif self.focus == "messages":
                    if key == curses.KEY_UP or key == ord("k"):
                        if self.message_scroll > 0:
                            self.message_scroll -= 1
                            self._draw_messages()
                    elif key == curses.KEY_DOWN or key == ord("j"):
                        self.message_scroll += 1
                        self._draw_messages()
                    elif key == curses.KEY_PPAGE:  # Page Up
                        self.message_scroll = max(0, self.message_scroll - 10)
                        self._draw_messages()
                    elif key == curses.KEY_NPAGE:  # Page Down
                        self.message_scroll += 10
                        self._draw_messages()
                    elif key == ord("g"):  # Go to top
                        self.message_scroll = 0
                        self._draw_messages()
                    elif key == ord("G"):  # Go to bottom
                        self.message_scroll = max(
                            0, len(self.messages) - self._visible_message_lines()
                        )
                        self._draw_messages()
                    elif key == curses.KEY_LEFT or key == ord("h"):
                        # Go back to conversations
                        if self.conv_win is not None:
                            self.focus = "conversations"
                            self._draw_status()
                            self._draw_conversations()
                    elif key == ord("i"):
                        self.focus = "input"
                        self._draw_status()
                        self._draw_input()
                    elif key == 9:  # Tab
                        self.focus = "conversations"
                        self._draw_status()
                    elif key == ord("r") or key == ord("R"):
                        # Refresh
                        if self.selected_chat_id:
                            self.messages = self.watcher.get_messages(
                                self.selected_chat_id
                            )
                            self.message_scroll = max(
                                0, len(self.messages) - self._visible_message_lines()
                            )
                        self._draw_messages()
                        self.status_message = "Refreshed"
                        self._draw_status()

        finally:
            self.watcher.stop()


def run_tui():
    """Run the TUI application."""

    def main(stdscr):
        # Setup curses
        try:
            curses.curs_set(0)
        except curses.error:
            pass

        # Create and run the TUI
        tui = MessagesTUI(stdscr)
        tui.run()

    # Run with curses wrapper for proper cleanup
    curses.wrapper(main)


if __name__ == "__main__":
    run_tui()

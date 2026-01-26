"""TUI (Text User Interface) for iMessage CLI using curses."""

import curses
import threading
import queue
import textwrap
from datetime import datetime
from typing import List, Optional, Tuple

from .watcher import MessageWatcher, Message, Conversation
from .sender import send_message


class InputBox:
    """A simple input box widget."""
    
    def __init__(self, window, y: int, x: int, width: int):
        self.window = window
        self.y = y
        self.x = x
        self.width = width
        self.text = ""
        self.cursor_pos = 0
        
    def handle_key(self, key: int) -> Optional[str]:
        """Handle a keypress. Returns the text if Enter is pressed."""
        if key == curses.KEY_BACKSPACE or key == 127 or key == 8:
            if self.cursor_pos > 0:
                self.text = self.text[:self.cursor_pos-1] + self.text[self.cursor_pos:]
                self.cursor_pos -= 1
        elif key == curses.KEY_DC:  # Delete
            if self.cursor_pos < len(self.text):
                self.text = self.text[:self.cursor_pos] + self.text[self.cursor_pos+1:]
        elif key == curses.KEY_LEFT:
            if self.cursor_pos > 0:
                self.cursor_pos -= 1
        elif key == curses.KEY_RIGHT:
            if self.cursor_pos < len(self.text):
                self.cursor_pos += 1
        elif key == curses.KEY_HOME or key == 1:  # Ctrl+A
            self.cursor_pos = 0
        elif key == curses.KEY_END or key == 5:  # Ctrl+E
            self.cursor_pos = len(self.text)
        elif key == 21:  # Ctrl+U - clear line
            self.text = ""
            self.cursor_pos = 0
        elif key == 10 or key == 13:  # Enter
            result = self.text
            self.text = ""
            self.cursor_pos = 0
            return result
        elif 32 <= key <= 126:  # Printable ASCII
            self.text = self.text[:self.cursor_pos] + chr(key) + self.text[self.cursor_pos:]
            self.cursor_pos += 1
        
        return None
    
    def draw(self, prompt: str = "> "):
        """Draw the input box."""
        display_width = self.width - len(prompt) - 1
        
        # Calculate visible portion of text
        if self.cursor_pos < display_width:
            visible_start = 0
        else:
            visible_start = self.cursor_pos - display_width + 1
        
        visible_text = self.text[visible_start:visible_start + display_width]
        cursor_x = self.cursor_pos - visible_start
        
        try:
            self.window.move(self.y, self.x)
            self.window.clrtoeol()
            self.window.addstr(self.y, self.x, prompt)
            self.window.addstr(visible_text)
            
            # Position cursor
            self.window.move(self.y, self.x + len(prompt) + cursor_x)
        except curses.error:
            pass


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
        self.focus = 'conversations'  # 'conversations', 'messages', 'input'
        self.input_box: Optional[InputBox] = None
        self.status_message = ""
        self.error_message = ""
        
        # Threading
        self.update_queue = queue.Queue()
        self.running = True
        
        # Setup
        self._setup_colors()
        self._setup_windows()
        self._setup_watcher()
        
    def _setup_colors(self):
        """Initialize color pairs."""
        curses.start_color()
        curses.use_default_colors()
        
        try:
            curses.init_pair(self.COLOR_NORMAL, -1, -1)
            curses.init_pair(self.COLOR_SELECTED, curses.COLOR_BLACK, curses.COLOR_WHITE)
            curses.init_pair(self.COLOR_SENT, curses.COLOR_GREEN, -1)
            curses.init_pair(self.COLOR_RECEIVED, curses.COLOR_CYAN, -1)
            curses.init_pair(self.COLOR_HEADER, curses.COLOR_BLACK, curses.COLOR_BLUE)
            curses.init_pair(self.COLOR_STATUS, curses.COLOR_BLACK, curses.COLOR_GREEN)
            curses.init_pair(self.COLOR_UNREAD, curses.COLOR_YELLOW, -1)
            curses.init_pair(self.COLOR_INPUT, curses.COLOR_WHITE, -1)
            curses.init_pair(self.COLOR_ERROR, curses.COLOR_RED, -1)
        except curses.error:
            pass
    
    def _setup_windows(self):
        """Create the window layout."""
        self.height, self.width = self.stdscr.getmaxyx()
        
        # Layout: [Conversations | Messages]
        #         [Status bar              ]
        #         [Input box               ]
        
        self.conv_width = min(35, self.width // 3)
        self.msg_width = self.width - self.conv_width
        self.msg_height = self.height - 3  # Leave room for status and input
        
        # Create windows
        self.conv_win = curses.newwin(self.msg_height, self.conv_width, 0, 0)
        self.msg_win = curses.newwin(self.msg_height, self.msg_width, 0, self.conv_width)
        self.status_win = curses.newwin(1, self.width, self.height - 2, 0)
        self.input_win = curses.newwin(1, self.width, self.height - 1, 0)
        
        # Enable scrolling
        self.conv_win.scrollok(True)
        self.msg_win.scrollok(True)
        
        # Input box
        self.input_box = InputBox(self.input_win, 0, 0, self.width)
        
        # Enable keypad
        self.stdscr.keypad(True)
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
        self.update_queue.put(('new_messages', messages))
        
    def _on_conversations_updated(self, conversations: List[Conversation]):
        """Callback when conversations are updated."""
        self.update_queue.put(('conversations', conversations))
        
    def _on_error(self, error: Exception):
        """Callback when an error occurs."""
        self.update_queue.put(('error', str(error)))
    
    def _process_updates(self):
        """Process any pending updates from the watcher."""
        try:
            while True:
                update_type, data = self.update_queue.get_nowait()
                
                if update_type == 'conversations':
                    self.conversations = data
                    self._draw_conversations()
                    
                elif update_type == 'new_messages':
                    # Check if any messages are for the current chat
                    for msg in data:
                        if msg.chat_id == self.selected_chat_id:
                            self.messages.append(msg)
                            # Auto-scroll to bottom
                            self.message_scroll = max(0, len(self.messages) - self._visible_message_lines())
                    self._draw_messages()
                    self._draw_conversations()  # Update unread counts
                    
                    # Flash notification
                    if data and not data[-1].is_from_me:
                        self.status_message = f"New message from {data[-1].sender}"
                        curses.flash()
                        
                elif update_type == 'error':
                    self.error_message = str(data)
                    
        except queue.Empty:
            pass
    
    def _visible_message_lines(self) -> int:
        """Calculate how many message lines are visible."""
        return self.msg_height - 2  # Account for header and border
    
    def _draw_header(self, win, title: str, color_pair: int):
        """Draw a header bar."""
        try:
            win.attron(curses.color_pair(color_pair))
            win.addstr(0, 0, title.center(win.getmaxyx()[1])[:win.getmaxyx()[1]-1])
            win.attroff(curses.color_pair(color_pair))
        except curses.error:
            pass
    
    def _draw_conversations(self):
        """Draw the conversations list."""
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
        for i, conv in enumerate(self.conversations[self.conv_scroll:self.conv_scroll + visible_height]):
            idx = i + self.conv_scroll
            y = i + 1
            
            if y >= self.msg_height - 1:
                break
            
            # Truncate name
            name = conv.display_name[:self.conv_width - 6]
            
            # Format time
            if conv.last_message_date:
                time_str = self._format_time(conv.last_message_date)
            else:
                time_str = ""
            
            # Build display line
            line = f" {name}"
            
            try:
                if idx == self.selected_conv_idx:
                    self.conv_win.attron(curses.color_pair(self.COLOR_SELECTED))
                    self.conv_win.addstr(y, 0, line.ljust(self.conv_width - 1)[:self.conv_width - 1])
                    self.conv_win.attroff(curses.color_pair(self.COLOR_SELECTED))
                else:
                    if conv.unread_count > 0:
                        self.conv_win.attron(curses.color_pair(self.COLOR_UNREAD) | curses.A_BOLD)
                        unread_indicator = f"({conv.unread_count})"
                        self.conv_win.addstr(y, 0, f" {unread_indicator} {name}"[:self.conv_width - 1])
                        self.conv_win.attroff(curses.color_pair(self.COLOR_UNREAD) | curses.A_BOLD)
                    else:
                        self.conv_win.addstr(y, 0, line[:self.conv_width - 1])
                    
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
            conv = next((c for c in self.conversations if c.chat_id == self.selected_chat_id), None)
            if conv:
                header = f" {conv.display_name} "
            else:
                header = " Messages "
        else:
            header = " Messages "
        
        self._draw_header(self.msg_win, header, self.COLOR_HEADER)
        
        if not self.messages:
            try:
                self.msg_win.addstr(2, 1, "Select a conversation" if not self.selected_chat_id else "No messages")
            except curses.error:
                pass
            self.msg_win.refresh()
            return
        
        # Calculate layout
        visible_height = self.msg_height - 2
        msg_display_width = self.msg_width - 4
        
        # Pre-render all message lines
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
            
            # Word wrap the message
            text = msg.text or ""
            text_lines = text.split('\n')
            
            first_line = True
            for text_line in text_lines:
                if first_line:
                    wrapped = textwrap.wrap(prefix + text_line, msg_display_width) or [prefix]
                    first_line = False
                else:
                    indent = " " * min(len(prefix), 20)
                    wrapped = textwrap.wrap(indent + text_line, msg_display_width) or [indent]
                
                for line in wrapped:
                    all_lines.append((line, color, msg.is_from_me))
        
        # Adjust scroll
        total_lines = len(all_lines)
        max_scroll = max(0, total_lines - visible_height)
        self.message_scroll = min(self.message_scroll, max_scroll)
        
        # Draw visible lines
        y = 1
        for line_text, color, is_from_me in all_lines[self.message_scroll:self.message_scroll + visible_height]:
            if y >= self.msg_height - 1:
                break
            
            try:
                self.msg_win.attron(curses.color_pair(color))
                self.msg_win.addstr(y, 1, line_text[:self.msg_width - 2])
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
            self.status_win.attron(curses.color_pair(self.COLOR_ERROR))
        elif self.status_message:
            status = f" {self.status_message}"
            self.status_win.attron(curses.color_pair(self.COLOR_STATUS))
        else:
            focus_indicator = {
                'conversations': '[CONV]',
                'messages': '[MSG]',
                'input': '[INPUT]'
            }.get(self.focus, '')
            
            help_text = "↑↓:Nav  Enter:Select  i:Input  Tab:Switch  q:Quit"
            status = f" {focus_indicator} {help_text}"
            self.status_win.attron(curses.color_pair(self.COLOR_STATUS))
        
        try:
            self.status_win.addstr(0, 0, status.ljust(self.width)[:self.width - 1])
        except curses.error:
            pass
        
        self.status_win.attroff(curses.color_pair(self.COLOR_STATUS))
        self.status_win.attroff(curses.color_pair(self.COLOR_ERROR))
        self.status_win.refresh()
        
        # Clear messages after a while
        self.status_message = ""
        self.error_message = ""
    
    def _draw_input(self):
        """Draw the input box."""
        self.input_win.erase()
        
        if self.focus == 'input':
            prompt = "Send: "
            self.input_box.draw(prompt)
            curses.curs_set(1)  # Show cursor
        else:
            curses.curs_set(0)  # Hide cursor
            try:
                hint = "Press 'i' to type a message, 'q' to quit"
                self.input_win.addstr(0, 0, hint, curses.A_DIM)
            except curses.error:
                pass
        
        self.input_win.refresh()
    
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
            self.message_scroll = max(0, len(self.messages) - self._visible_message_lines())
            
            self._draw_conversations()
            self._draw_messages()
    
    def _send_current_message(self, text: str):
        """Send the current message."""
        if not text.strip():
            return
        
        if not self.selected_chat_id or not self.conversations:
            self.error_message = "No conversation selected"
            return
        
        conv = next((c for c in self.conversations if c.chat_id == self.selected_chat_id), None)
        if not conv:
            self.error_message = "Conversation not found"
            return
        
        try:
            success = send_message(conv.chat_identifier, text)
            if success:
                self.status_message = "Message sent!"
            else:
                self.error_message = "Failed to send message"
        except Exception as e:
            self.error_message = f"Send error: {str(e)[:30]}"
    
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
                
                # Handle input based on focus
                if self.focus == 'input':
                    if key == 27:  # Escape
                        self.focus = 'messages'
                        self._draw_status()
                        self._draw_input()
                    else:
                        result = self.input_box.handle_key(key)
                        if result is not None:
                            self._send_current_message(result)
                            self.focus = 'messages'
                            self._draw_status()
                        self._draw_input()
                
                elif self.focus == 'conversations':
                    if key == ord('q') or key == ord('Q'):
                        self.running = False
                    elif key == curses.KEY_UP or key == ord('k'):
                        if self.selected_conv_idx > 0:
                            self._select_conversation(self.selected_conv_idx - 1)
                    elif key == curses.KEY_DOWN or key == ord('j'):
                        if self.selected_conv_idx < len(self.conversations) - 1:
                            self._select_conversation(self.selected_conv_idx + 1)
                    elif key == 10 or key == 13 or key == curses.KEY_RIGHT or key == ord('l'):
                        self.focus = 'messages'
                        self._draw_status()
                        self._draw_conversations()
                    elif key == ord('i'):
                        self.focus = 'input'
                        self._draw_status()
                        self._draw_input()
                    elif key == 9:  # Tab
                        self.focus = 'messages'
                        self._draw_status()
                    elif key == ord('r') or key == ord('R'):
                        # Refresh
                        self.conversations = self.watcher.get_conversations()
                        if self.selected_chat_id:
                            self.messages = self.watcher.get_messages(self.selected_chat_id)
                        self.draw_all()
                        self.status_message = "Refreshed"
                        self._draw_status()
                
                elif self.focus == 'messages':
                    if key == ord('q') or key == ord('Q'):
                        self.running = False
                    elif key == curses.KEY_UP or key == ord('k'):
                        if self.message_scroll > 0:
                            self.message_scroll -= 1
                            self._draw_messages()
                    elif key == curses.KEY_DOWN or key == ord('j'):
                        self.message_scroll += 1
                        self._draw_messages()
                    elif key == curses.KEY_PPAGE:  # Page Up
                        self.message_scroll = max(0, self.message_scroll - 10)
                        self._draw_messages()
                    elif key == curses.KEY_NPAGE:  # Page Down
                        self.message_scroll += 10
                        self._draw_messages()
                    elif key == ord('g'):  # Go to top
                        self.message_scroll = 0
                        self._draw_messages()
                    elif key == ord('G'):  # Go to bottom
                        self.message_scroll = max(0, len(self.messages) - self._visible_message_lines())
                        self._draw_messages()
                    elif key == curses.KEY_LEFT or key == ord('h'):
                        self.focus = 'conversations'
                        self._draw_status()
                        self._draw_conversations()
                    elif key == ord('i'):
                        self.focus = 'input'
                        self._draw_status()
                        self._draw_input()
                    elif key == 9:  # Tab
                        self.focus = 'conversations'
                        self._draw_status()
                    elif key == ord('r') or key == ord('R'):
                        # Refresh
                        if self.selected_chat_id:
                            self.messages = self.watcher.get_messages(self.selected_chat_id)
                            self.message_scroll = max(0, len(self.messages) - self._visible_message_lines())
                        self._draw_messages()
                        self.status_message = "Refreshed"
                        self._draw_status()
        
        finally:
            self.watcher.stop()


def run_tui():
    """Run the TUI application."""
    def main(stdscr):
        # Setup curses
        curses.curs_set(0)
        
        # Create and run the TUI
        tui = MessagesTUI(stdscr)
        tui.run()
    
    # Run with curses wrapper for proper cleanup
    curses.wrapper(main)


if __name__ == '__main__':
    run_tui()

"""Database watcher for real-time message updates."""

import os
import sqlite3
import threading
import time
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Callable, List, Optional

from .database import get_db_path, get_connection, apple_time_to_datetime, extract_text_from_attributed_body


@dataclass
class Message:
    """Represents an iMessage."""
    message_id: int
    text: str
    date: Optional[datetime]
    is_from_me: bool
    is_read: bool
    sender: str
    chat_id: int
    chat_identifier: str
    chat_name: str


@dataclass
class Conversation:
    """Represents a conversation/chat."""
    chat_id: int
    chat_identifier: str
    display_name: str
    service: str
    last_message_date: Optional[datetime]
    last_message_text: str
    unread_count: int
    participants: List[str]


class MessageWatcher:
    """Watches the iMessage database for new messages."""
    
    def __init__(self, poll_interval: float = 1.0):
        """Initialize the watcher.
        
        Args:
            poll_interval: How often to check for new messages (seconds)
        """
        self.poll_interval = poll_interval
        self._running = False
        self._thread: Optional[threading.Thread] = None
        self._last_message_id = 0
        self._last_mtime = 0.0
        self._callbacks: List[Callable[[List[Message]], None]] = []
        self._conversation_callbacks: List[Callable[[List[Conversation]], None]] = []
        self._error_callbacks: List[Callable[[Exception], None]] = []
        
    def on_new_messages(self, callback: Callable[[List[Message]], None]):
        """Register a callback for new messages."""
        self._callbacks.append(callback)
        
    def on_conversations_updated(self, callback: Callable[[List[Conversation]], None]):
        """Register a callback for conversation updates."""
        self._conversation_callbacks.append(callback)
        
    def on_error(self, callback: Callable[[Exception], None]):
        """Register a callback for errors."""
        self._error_callbacks.append(callback)
    
    def _get_last_message_id(self) -> int:
        """Get the ID of the most recent message."""
        try:
            conn = get_connection()
            cursor = conn.cursor()
            cursor.execute("SELECT MAX(ROWID) FROM message")
            result = cursor.fetchone()
            conn.close()
            return result[0] or 0
        except Exception:
            return 0
    
    def _get_db_mtime(self) -> float:
        """Get the modification time of the database file."""
        try:
            db_path = get_db_path()
            return os.path.getmtime(db_path)
        except Exception:
            return 0.0
    
    def get_conversations(self, limit: int = 50) -> List[Conversation]:
        """Get list of conversations with metadata."""
        conn = get_connection()
        cursor = conn.cursor()
        
        query = """
        SELECT 
            c.ROWID as chat_id,
            c.chat_identifier,
            c.display_name,
            c.service_name,
            MAX(m.date) as last_message_date,
            (SELECT text FROM message m2 
             JOIN chat_message_join cmj2 ON m2.ROWID = cmj2.message_id 
             WHERE cmj2.chat_id = c.ROWID 
             ORDER BY m2.date DESC LIMIT 1) as last_message_text,
            SUM(CASE WHEN m.is_read = 0 AND m.is_from_me = 0 THEN 1 ELSE 0 END) as unread_count,
            GROUP_CONCAT(DISTINCT h.id) as participants
        FROM chat c
        LEFT JOIN chat_message_join cmj ON c.ROWID = cmj.chat_id
        LEFT JOIN message m ON cmj.message_id = m.ROWID
        LEFT JOIN chat_handle_join chj ON c.ROWID = chj.chat_id
        LEFT JOIN handle h ON chj.handle_id = h.ROWID
        GROUP BY c.ROWID
        ORDER BY last_message_date DESC
        LIMIT ?
        """
        
        cursor.execute(query, (limit,))
        rows = cursor.fetchall()
        conn.close()
        
        conversations = []
        for row in rows:
            last_date = apple_time_to_datetime(row[4])
            conversations.append(Conversation(
                chat_id=row[0],
                chat_identifier=row[1],
                display_name=row[2] or row[1] or "Unknown",
                service=row[3] or "iMessage",
                last_message_date=last_date,
                last_message_text=row[5] or "",
                unread_count=row[6] or 0,
                participants=row[7].split(',') if row[7] else []
            ))
        
        return conversations
    
    def get_messages(self, chat_id: int, limit: int = 100) -> List[Message]:
        """Get messages for a specific chat."""
        conn = get_connection()
        cursor = conn.cursor()
        
        query = """
        SELECT 
            m.ROWID as message_id,
            m.text,
            m.attributedBody,
            m.date,
            m.is_from_me,
            m.is_read,
            h.id as sender_id,
            c.ROWID as chat_id,
            c.chat_identifier,
            c.display_name
        FROM message m
        LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
        LEFT JOIN chat c ON cmj.chat_id = c.ROWID
        LEFT JOIN handle h ON m.handle_id = h.ROWID
        WHERE c.ROWID = ?
        ORDER BY m.date DESC
        LIMIT ?
        """
        
        cursor.execute(query, (chat_id, limit))
        rows = cursor.fetchall()
        conn.close()
        
        messages = []
        for row in rows:
            msg_date = apple_time_to_datetime(row[3])
            
            # Try to get text from the text column first, then fall back to attributedBody
            text = row[1]
            if not text and row[2]:
                text = extract_text_from_attributed_body(row[2])
            if not text:
                text = "[Attachment]"
            
            messages.append(Message(
                message_id=row[0],
                text=text,
                date=msg_date,
                is_from_me=bool(row[4]),
                is_read=bool(row[5]),
                sender='Me' if row[4] else (row[6] or 'Unknown'),
                chat_id=row[7],
                chat_identifier=row[8],
                chat_name=row[9] or row[8] or "Unknown"
            ))
        
        # Return in chronological order
        messages.reverse()
        return messages
    
    def get_new_messages(self, since_id: int) -> List[Message]:
        """Get messages newer than the given ID."""
        conn = get_connection()
        cursor = conn.cursor()
        
        query = """
        SELECT 
            m.ROWID as message_id,
            m.text,
            m.attributedBody,
            m.date,
            m.is_from_me,
            m.is_read,
            h.id as sender_id,
            c.ROWID as chat_id,
            c.chat_identifier,
            c.display_name
        FROM message m
        LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
        LEFT JOIN chat c ON cmj.chat_id = c.ROWID
        LEFT JOIN handle h ON m.handle_id = h.ROWID
        WHERE m.ROWID > ?
        ORDER BY m.date ASC
        """
        
        cursor.execute(query, (since_id,))
        rows = cursor.fetchall()
        conn.close()
        
        messages = []
        for row in rows:
            msg_date = apple_time_to_datetime(row[3])
            
            # Try to get text from the text column first, then fall back to attributedBody
            text = row[1]
            if not text and row[2]:
                text = extract_text_from_attributed_body(row[2])
            if not text:
                text = "[Attachment]"
            
            messages.append(Message(
                message_id=row[0],
                text=text,
                date=msg_date,
                is_from_me=bool(row[4]),
                is_read=bool(row[5]),
                sender='Me' if row[4] else (row[6] or 'Unknown'),
                chat_id=row[7],
                chat_identifier=row[8],
                chat_name=row[9] or row[8] or "Unknown"
            ))
        
        return messages
    
    def _poll_loop(self):
        """Main polling loop."""
        while self._running:
            try:
                # Check if database has been modified
                current_mtime = self._get_db_mtime()
                
                if current_mtime > self._last_mtime:
                    self._last_mtime = current_mtime
                    
                    # Check for new messages
                    current_max_id = self._get_last_message_id()
                    
                    if current_max_id > self._last_message_id:
                        new_messages = self.get_new_messages(self._last_message_id)
                        self._last_message_id = current_max_id
                        
                        if new_messages:
                            for callback in self._callbacks:
                                try:
                                    callback(new_messages)
                                except Exception as e:
                                    self._notify_error(e)
                    
                    # Update conversations
                    conversations = self.get_conversations()
                    for callback in self._conversation_callbacks:
                        try:
                            callback(conversations)
                        except Exception as e:
                            self._notify_error(e)
                            
            except Exception as e:
                self._notify_error(e)
            
            time.sleep(self.poll_interval)
    
    def _notify_error(self, error: Exception):
        """Notify error callbacks."""
        for callback in self._error_callbacks:
            try:
                callback(error)
            except Exception:
                pass
    
    def start(self):
        """Start watching for new messages."""
        if self._running:
            return
        
        # Initialize state
        self._last_message_id = self._get_last_message_id()
        self._last_mtime = self._get_db_mtime()
        self._running = True
        
        self._thread = threading.Thread(target=self._poll_loop, daemon=True)
        self._thread.start()
    
    def stop(self):
        """Stop watching for messages."""
        self._running = False
        if self._thread:
            self._thread.join(timeout=2.0)
            self._thread = None
    
    def __enter__(self):
        self.start()
        return self
    
    def __exit__(self, exc_type, exc_val, exc_tb):
        self.stop()
        return False

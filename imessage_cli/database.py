"""Database module for reading iMessage data from chat.db."""

import os
import sqlite3
from datetime import datetime
from pathlib import Path
from typing import List, Optional, Tuple

# Import contact resolver (lazy import to avoid circular imports)
def _get_contact_name(identifier: str) -> str:
    """Get contact name with lazy import to avoid circular imports."""
    from .contacts import get_contact_name
    return get_contact_name(identifier)


def extract_text_from_attributed_body(attributed_body: bytes) -> Optional[str]:
    """Extract plain text from an attributedBody blob.
    
    The attributedBody column contains a serialized NSAttributedString.
    This function uses heuristics to extract the plain text content.
    
    Args:
        attributed_body: The raw bytes from the attributedBody column
        
    Returns:
        The extracted text, or None if extraction fails
    """
    if attributed_body is None:
        return None
    
    try:
        # Decode as UTF-8, replacing invalid characters
        decoded = attributed_body.decode('utf-8', errors='replace')
        
        # Method 1: The attributed body contains serialized NSAttributedString data
        # The actual text is typically between NSString and NSDictionary/NSNumber markers
        if "NSNumber" in decoded:
            temp = decoded.split("NSNumber")[0]
            if "NSString" in temp:
                temp = temp.split("NSString")[1]
                if "NSDictionary" in temp:
                    temp = temp.split("NSDictionary")[0]
                    # Remove leading/trailing serialization bytes
                    # The format is typically: some_bytes + text + some_bytes
                    text = temp[6:-12] if len(temp) > 18 else temp
                    # Clean up the text
                    cleaned = ''.join(c for c in text if c.isprintable() or c in '\n\t')
                    if cleaned.strip():
                        return cleaned.strip()
        
        # Method 2: Try to find text after streamtyped marker
        if b'streamtyped' in attributed_body:
            # The text often appears after certain byte sequences
            parts = attributed_body.split(b'NSString')
            if len(parts) > 1:
                # Take content after NSString marker
                text_part = parts[1]
                # Decode and clean up
                text = text_part.decode('utf-8', errors='replace')
                # Remove non-printable characters and trim
                cleaned = ''.join(c for c in text if c.isprintable() or c in '\n\t')
                # Find where the actual text ends (before next marker)
                for marker in ['NSDictionary', 'NSNumber', 'NSArray']:
                    if marker in cleaned:
                        cleaned = cleaned.split(marker)[0]
                cleaned = cleaned.strip()
                if len(cleaned) > 1:
                    return cleaned
        
        # Method 3: Look for any readable text between common delimiters
        # Try to find the longest sequence of printable characters
        import re
        # Find sequences of printable ASCII/Unicode characters
        matches = re.findall(r'[\x20-\x7E\u00A0-\uFFFF]{3,}', decoded)
        if matches:
            # Filter out known serialization artifacts
            filtered = [m for m in matches if not any(
                marker in m for marker in ['bplist', 'NSString', 'NSNumber', 'NSDictionary', 
                                           'NSArray', 'NSData', '$class', 'archiver', 'streamtyped']
            )]
            if filtered:
                # Return the longest match that looks like actual content
                candidates = [m.strip() for m in filtered if len(m.strip()) > 2]
                if candidates:
                    return max(candidates, key=len)
        
        return None
        
    except Exception:
        return None


def get_db_path() -> Path:
    """Get the path to the iMessage database."""
    return Path.home() / "Library" / "Messages" / "chat.db"


def get_connection() -> sqlite3.Connection:
    """Create a read-only connection to the iMessage database."""
    db_path = get_db_path()
    if not db_path.exists():
        raise FileNotFoundError(
            f"iMessage database not found at {db_path}. "
            "Make sure you're running this on macOS with Messages configured."
        )
    
    # Connect in read-only mode using URI
    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def apple_time_to_datetime(apple_time: int) -> datetime:
    """Convert Apple's timestamp format to Python datetime.
    
    Apple uses nanoseconds since 2001-01-01, while Unix uses seconds since 1970-01-01.
    The difference is 978307200 seconds.
    """
    if apple_time is None:
        return None
    
    # Apple timestamps can be in different formats depending on macOS version
    # Modern versions use nanoseconds (very large numbers)
    # Older versions used seconds
    if apple_time > 1e18:  # Nanoseconds
        unix_timestamp = (apple_time / 1e9) + 978307200
    elif apple_time > 1e9:  # Already in reasonable range, might be nanoseconds
        unix_timestamp = (apple_time / 1e9) + 978307200
    else:
        unix_timestamp = apple_time + 978307200
    
    try:
        return datetime.fromtimestamp(unix_timestamp)
    except (OSError, ValueError):
        return None


def get_conversations(limit: int = 50) -> List[dict]:
    """Get a list of recent conversations.
    
    Returns:
        List of dicts with conversation info including chat_id, display_name,
        participant identifiers, and last message date.
    """
    conn = get_connection()
    cursor = conn.cursor()
    
    query = """
    SELECT 
        c.ROWID as chat_id,
        c.chat_identifier,
        c.display_name,
        c.service_name,
        MAX(m.date) as last_message_date,
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
        last_date = apple_time_to_datetime(row['last_message_date'])
        chat_identifier = row['chat_identifier'] or ""
        
        # Use existing display_name or resolve from contacts
        display_name = row['display_name']
        if not display_name:
            display_name = _get_contact_name(chat_identifier)
        
        conversations.append({
            'chat_id': row['chat_id'],
            'chat_identifier': chat_identifier,
            'display_name': display_name,
            'service': row['service_name'],
            'last_message_date': last_date,
            'participants': row['participants'].split(',') if row['participants'] else []
        })
    
    return conversations


def get_messages(
    chat_identifier: Optional[str] = None,
    chat_id: Optional[int] = None,
    limit: int = 50,
    before_date: Optional[datetime] = None
) -> List[dict]:
    """Get messages from a specific conversation.
    
    Args:
        chat_identifier: The chat identifier (phone number, email, or group ID)
        chat_id: The internal chat ID (ROWID)
        limit: Maximum number of messages to return
        before_date: Only return messages before this date
        
    Returns:
        List of message dicts with text, sender, date, etc.
    """
    conn = get_connection()
    cursor = conn.cursor()
    
    if chat_id:
        where_clause = "c.ROWID = ?"
        where_param = chat_id
    elif chat_identifier:
        where_clause = "c.chat_identifier = ?"
        where_param = chat_identifier
    else:
        raise ValueError("Must provide either chat_identifier or chat_id")
    
    query = f"""
    SELECT 
        m.ROWID as message_id,
        m.text,
        m.attributedBody,
        m.date,
        m.is_from_me,
        m.is_read,
        m.service,
        h.id as sender_id,
        COALESCE(m.cache_roomnames, '') as group_name
    FROM message m
    LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
    LEFT JOIN chat c ON cmj.chat_id = c.ROWID
    LEFT JOIN handle h ON m.handle_id = h.ROWID
    WHERE {where_clause}
    ORDER BY m.date DESC
    LIMIT ?
    """
    
    cursor.execute(query, (where_param, limit))
    rows = cursor.fetchall()
    conn.close()
    
    messages = []
    for row in rows:
        msg_date = apple_time_to_datetime(row['date'])
        
        # Try to get text from the text column first, then fall back to attributedBody
        text = row['text']
        if not text and row['attributedBody']:
            text = extract_text_from_attributed_body(row['attributedBody'])
        
        # Final fallback if no text could be extracted
        if not text:
            text = '[Attachment]'
        
        # Resolve sender to contact name
        sender_id = row['sender_id']
        if row['is_from_me']:
            sender = 'Me'
        elif sender_id:
            sender = _get_contact_name(sender_id)
        else:
            sender = 'Unknown'
        
        messages.append({
            'message_id': row['message_id'],
            'text': text,
            'date': msg_date,
            'is_from_me': bool(row['is_from_me']),
            'is_read': bool(row['is_read']),
            'service': row['service'],
            'sender': sender,
        })
    
    # Reverse to show oldest first
    messages.reverse()
    return messages


def search_messages(query: str, limit: int = 50) -> List[dict]:
    """Search for messages containing the given text.
    
    Args:
        query: Text to search for
        limit: Maximum number of results
        
    Returns:
        List of matching messages with conversation context
    """
    conn = get_connection()
    cursor = conn.cursor()
    
    # Search in the text column. For attributedBody, we need to cast to text first.
    # Using CAST(attributedBody AS TEXT) allows partial matching of text embedded in the blob.
    sql = """
    SELECT 
        m.ROWID as message_id,
        m.text,
        m.attributedBody,
        m.date,
        m.is_from_me,
        c.chat_identifier,
        c.display_name,
        h.id as sender_id
    FROM message m
    LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
    LEFT JOIN chat c ON cmj.chat_id = c.ROWID
    LEFT JOIN handle h ON m.handle_id = h.ROWID
    WHERE m.text LIKE ? OR CAST(m.attributedBody AS TEXT) LIKE ?
    ORDER BY m.date DESC
    LIMIT ?
    """
    
    search_pattern = f'%{query}%'
    cursor.execute(sql, (search_pattern, search_pattern, limit))
    rows = cursor.fetchall()
    conn.close()
    
    results = []
    for row in rows:
        msg_date = apple_time_to_datetime(row['date'])
        
        # Try to get text from the text column first, then fall back to attributedBody
        text = row['text']
        if not text and row['attributedBody']:
            text = extract_text_from_attributed_body(row['attributedBody'])
        
        if not text:
            text = '[Attachment]'
        
        # Resolve sender to contact name
        sender_id = row['sender_id']
        if row['is_from_me']:
            sender = 'Me'
        elif sender_id:
            sender = _get_contact_name(sender_id)
        else:
            sender = 'Unknown'
        
        # Resolve chat name
        chat_identifier = row['chat_identifier'] or ""
        chat_name = row['display_name']
        if not chat_name:
            chat_name = _get_contact_name(chat_identifier)
        
        results.append({
            'message_id': row['message_id'],
            'text': text,
            'date': msg_date,
            'is_from_me': bool(row['is_from_me']),
            'chat_identifier': chat_identifier,
            'chat_name': chat_name,
            'sender': sender,
        })
    
    return results


def get_unread_count() -> int:
    """Get the count of unread messages."""
    conn = get_connection()
    cursor = conn.cursor()
    
    cursor.execute("""
        SELECT COUNT(*) as count 
        FROM message 
        WHERE is_read = 0 AND is_from_me = 0
    """)
    
    result = cursor.fetchone()
    conn.close()
    
    return result['count'] if result else 0


def get_contact_by_identifier(identifier: str) -> Optional[dict]:
    """Look up a contact by phone number or email.
    
    Args:
        identifier: Phone number or email address
        
    Returns:
        Contact info dict or None if not found
    """
    conn = get_connection()
    cursor = conn.cursor()
    
    # Normalize phone number (remove spaces, dashes, parentheses)
    normalized = ''.join(c for c in identifier if c.isdigit() or c in '+@.')
    
    cursor.execute("""
        SELECT DISTINCT 
            h.id as identifier,
            h.service,
            c.chat_identifier,
            c.display_name
        FROM handle h
        LEFT JOIN chat_handle_join chj ON h.ROWID = chj.handle_id
        LEFT JOIN chat c ON chj.chat_id = c.ROWID
        WHERE h.id LIKE ? OR h.id LIKE ?
        LIMIT 1
    """, (f'%{identifier}%', f'%{normalized}%'))
    
    row = cursor.fetchone()
    conn.close()
    
    if row:
        return {
            'identifier': row['identifier'],
            'service': row['service'],
            'chat_identifier': row['chat_identifier'],
            'display_name': row['display_name']
        }
    return None

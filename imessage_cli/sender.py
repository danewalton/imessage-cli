"""Module for sending iMessages using AppleScript."""

import subprocess
import shlex
from typing import Optional


def send_message(recipient: str, message: str, service: str = "iMessage") -> bool:
    """Send an iMessage to a recipient.
    
    Args:
        recipient: Phone number or email address of the recipient
        message: The message text to send
        service: The service to use ("iMessage" or "SMS")
        
    Returns:
        True if message was sent successfully, False otherwise
        
    Raises:
        RuntimeError: If AppleScript execution fails
    """
    # Escape special characters in the message for AppleScript
    escaped_message = message.replace('\\', '\\\\').replace('"', '\\"')
    escaped_recipient = recipient.replace('\\', '\\\\').replace('"', '\\"')
    
    applescript = f'''
    tell application "Messages"
        set targetService to 1st service whose service type = {service}
        set targetBuddy to buddy "{escaped_recipient}" of targetService
        send "{escaped_message}" to targetBuddy
    end tell
    '''
    
    try:
        result = subprocess.run(
            ['osascript', '-e', applescript],
            capture_output=True,
            text=True,
            timeout=30
        )
        
        if result.returncode != 0:
            # Try alternative method using participant specifier
            return _send_message_alternative(recipient, message)
        
        return True
        
    except subprocess.TimeoutExpired:
        raise RuntimeError("Timeout while sending message")
    except FileNotFoundError:
        raise RuntimeError(
            "osascript not found. This tool requires macOS with AppleScript support."
        )


def _send_message_alternative(recipient: str, message: str) -> bool:
    """Alternative method to send message using chat specifier.
    
    This method creates a new chat if one doesn't exist.
    """
    escaped_message = message.replace('\\', '\\\\').replace('"', '\\"')
    escaped_recipient = recipient.replace('\\', '\\\\').replace('"', '\\"')
    
    applescript = f'''
    tell application "Messages"
        send "{escaped_message}" to participant "{escaped_recipient}" of (1st chat whose participants contains participant "{escaped_recipient}")
    end tell
    '''
    
    try:
        result = subprocess.run(
            ['osascript', '-e', applescript],
            capture_output=True,
            text=True,
            timeout=30
        )
        
        if result.returncode != 0:
            # Final fallback: start new message
            return _send_new_message(recipient, message)
        
        return True
        
    except subprocess.TimeoutExpired:
        raise RuntimeError("Timeout while sending message")


def _send_new_message(recipient: str, message: str) -> bool:
    """Send a message by creating a new conversation."""
    escaped_message = message.replace('\\', '\\\\').replace('"', '\\"')
    escaped_recipient = recipient.replace('\\', '\\\\').replace('"', '\\"')
    
    applescript = f'''
    tell application "Messages"
        set theBuddy to "{escaped_recipient}"
        set theMessage to "{escaped_message}"
        set theService to 1st account whose service type = iMessage
        set theParticipant to participant theBuddy of theService
        send theMessage to theParticipant
    end tell
    '''
    
    try:
        result = subprocess.run(
            ['osascript', '-e', applescript],
            capture_output=True,
            text=True,
            timeout=30
        )
        
        if result.returncode != 0:
            error_msg = result.stderr.strip() or "Unknown error"
            raise RuntimeError(f"Failed to send message: {error_msg}")
        
        return True
        
    except subprocess.TimeoutExpired:
        raise RuntimeError("Timeout while sending message")


def send_to_group(chat_name: str, message: str) -> bool:
    """Send a message to a group chat by name.
    
    Args:
        chat_name: The display name of the group chat
        message: The message text to send
        
    Returns:
        True if message was sent successfully
        
    Raises:
        RuntimeError: If sending fails
    """
    escaped_message = message.replace('\\', '\\\\').replace('"', '\\"')
    escaped_name = chat_name.replace('\\', '\\\\').replace('"', '\\"')
    
    applescript = f'''
    tell application "Messages"
        set theChat to 1st chat whose name = "{escaped_name}"
        send "{escaped_message}" to theChat
    end tell
    '''
    
    try:
        result = subprocess.run(
            ['osascript', '-e', applescript],
            capture_output=True,
            text=True,
            timeout=30
        )
        
        if result.returncode != 0:
            error_msg = result.stderr.strip() or "Unknown error"
            raise RuntimeError(f"Failed to send to group: {error_msg}")
        
        return True
        
    except subprocess.TimeoutExpired:
        raise RuntimeError("Timeout while sending message")


def check_messages_running() -> bool:
    """Check if the Messages app is running.
    
    Returns:
        True if Messages is running, False otherwise
    """
    applescript = '''
    tell application "System Events"
        return (name of processes) contains "Messages"
    end tell
    '''
    
    try:
        result = subprocess.run(
            ['osascript', '-e', applescript],
            capture_output=True,
            text=True,
            timeout=10
        )
        
        return result.stdout.strip().lower() == 'true'
        
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return False


def start_messages_app() -> bool:
    """Start the Messages app if it's not running.
    
    Returns:
        True if Messages started successfully or was already running
    """
    applescript = '''
    tell application "Messages"
        activate
    end tell
    '''
    
    try:
        result = subprocess.run(
            ['osascript', '-e', applescript],
            capture_output=True,
            text=True,
            timeout=30
        )
        
        return result.returncode == 0
        
    except (subprocess.TimeoutExpired, FileNotFoundError):
        return False

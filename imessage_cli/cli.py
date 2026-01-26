#!/usr/bin/env python3
"""Command-line interface for iMessage CLI."""

import argparse
import sys
from datetime import datetime
from typing import Optional

from . import __version__
from .database import (
    get_conversations,
    get_messages,
    search_messages,
    get_unread_count,
    get_contact_by_identifier,
)
from .sender import send_message, send_to_group, check_messages_running, start_messages_app
from .tui import run_tui


# ANSI color codes for terminal output
class Colors:
    RESET = '\033[0m'
    BOLD = '\033[1m'
    DIM = '\033[2m'
    
    RED = '\033[91m'
    GREEN = '\033[92m'
    YELLOW = '\033[93m'
    BLUE = '\033[94m'
    MAGENTA = '\033[95m'
    CYAN = '\033[96m'
    WHITE = '\033[97m'
    
    BG_BLUE = '\033[44m'
    BG_GREEN = '\033[42m'


def colored(text: str, *colors) -> str:
    """Apply ANSI colors to text."""
    if not sys.stdout.isatty():
        return text
    return ''.join(colors) + text + Colors.RESET


def format_date(dt: Optional[datetime]) -> str:
    """Format a datetime for display."""
    if dt is None:
        return "Unknown"
    
    now = datetime.now()
    diff = now - dt
    
    if diff.days == 0:
        return dt.strftime("%I:%M %p")
    elif diff.days == 1:
        return "Yesterday " + dt.strftime("%I:%M %p")
    elif diff.days < 7:
        return dt.strftime("%A %I:%M %p")
    else:
        return dt.strftime("%Y-%m-%d %I:%M %p")


def truncate(text: str, max_length: int = 50) -> str:
    """Truncate text to max length with ellipsis."""
    if text is None:
        return ""
    text = text.replace('\n', ' ').strip()
    if len(text) <= max_length:
        return text
    return text[:max_length - 3] + "..."


def cmd_list(args):
    """List recent conversations."""
    try:
        conversations = get_conversations(limit=args.limit)
        
        if not conversations:
            print("No conversations found.")
            return
        
        print(colored(f"\n{'#':<4} {'Contact':<30} {'Last Message':<20} {'Service':<10}", 
                     Colors.BOLD, Colors.CYAN))
        print("-" * 70)
        
        for i, conv in enumerate(conversations, 1):
            name = truncate(conv['display_name'], 28)
            date_str = format_date(conv['last_message_date'])
            service = conv['service'] or 'iMessage'
            
            # Color code based on service
            if 'SMS' in service:
                service_color = Colors.GREEN
            else:
                service_color = Colors.BLUE
            
            print(f"{i:<4} {name:<30} {date_str:<20} {colored(service, service_color):<10}")
        
        # Show unread count
        unread = get_unread_count()
        if unread > 0:
            print(colored(f"\n📬 {unread} unread message(s)", Colors.YELLOW, Colors.BOLD))
        
        print(colored("\nTip: Use 'imessage read <number>' to view messages from a conversation", 
                     Colors.DIM))
        
    except FileNotFoundError as e:
        print(colored(f"Error: {e}", Colors.RED))
        sys.exit(1)
    except Exception as e:
        print(colored(f"Error reading messages: {e}", Colors.RED))
        sys.exit(1)


def cmd_read(args):
    """Read messages from a conversation."""
    try:
        conversations = get_conversations(limit=100)
        
        # Determine which conversation to read
        chat_id = None
        chat_identifier = None
        chat_name = None
        
        if args.conversation.isdigit():
            # User provided a number from the list
            idx = int(args.conversation) - 1
            if 0 <= idx < len(conversations):
                conv = conversations[idx]
                chat_id = conv['chat_id']
                chat_name = conv['display_name']
            else:
                print(colored(f"Invalid conversation number. Use 1-{len(conversations)}", Colors.RED))
                sys.exit(1)
        else:
            # User provided a phone number or identifier
            chat_identifier = args.conversation
            contact = get_contact_by_identifier(chat_identifier)
            if contact:
                chat_identifier = contact['chat_identifier'] or chat_identifier
                chat_name = contact['display_name'] or chat_identifier
            else:
                chat_name = chat_identifier
        
        # Get messages
        if chat_id:
            messages = get_messages(chat_id=chat_id, limit=args.limit)
        else:
            messages = get_messages(chat_identifier=chat_identifier, limit=args.limit)
        
        if not messages:
            print(f"No messages found for {chat_name}")
            return
        
        print(colored(f"\n📱 Messages with {chat_name}", Colors.BOLD, Colors.CYAN))
        print("-" * 60)
        
        for msg in messages:
            date_str = format_date(msg['date'])
            sender = msg['sender']
            text = msg['text'] or "[No text content]"
            
            if msg['is_from_me']:
                # Right-align sent messages (simulated)
                print(colored(f"\n{date_str:>58}", Colors.DIM))
                print(colored(f"{'Me:':>10} ", Colors.GREEN, Colors.BOLD) + text)
            else:
                print(colored(f"\n{date_str}", Colors.DIM))
                print(colored(f"{sender}: ", Colors.BLUE, Colors.BOLD) + text)
        
        print("\n" + "-" * 60)
        
        # Show hint for replying
        reply_target = chat_identifier or str(args.conversation)
        print(colored(f"Reply: imessage send \"{reply_target}\" \"your message\"", Colors.DIM))
        
    except FileNotFoundError as e:
        print(colored(f"Error: {e}", Colors.RED))
        sys.exit(1)
    except ValueError as e:
        print(colored(f"Error: {e}", Colors.RED))
        sys.exit(1)
    except Exception as e:
        print(colored(f"Error reading messages: {e}", Colors.RED))
        sys.exit(1)


def cmd_send(args):
    """Send a message."""
    recipient = args.recipient
    message = args.message
    
    # Confirm before sending
    if not args.yes:
        print(colored(f"Sending to: ", Colors.BOLD) + recipient)
        print(colored(f"Message: ", Colors.BOLD) + message)
        confirm = input(colored("\nSend this message? [y/N] ", Colors.YELLOW))
        if confirm.lower() not in ('y', 'yes'):
            print("Message cancelled.")
            return
    
    try:
        # Check if Messages app is accessible
        print("Sending message...")
        
        success = send_message(recipient, message)
        
        if success:
            print(colored("✓ Message sent successfully!", Colors.GREEN, Colors.BOLD))
        else:
            print(colored("✗ Failed to send message", Colors.RED))
            sys.exit(1)
            
    except RuntimeError as e:
        print(colored(f"Error: {e}", Colors.RED))
        print(colored("\nMake sure:", Colors.YELLOW))
        print("  1. Messages app is configured and signed in")
        print("  2. You've granted Terminal/SSH full disk access in System Preferences")
        print("  3. The recipient is a valid phone number or email")
        sys.exit(1)
    except Exception as e:
        print(colored(f"Unexpected error: {e}", Colors.RED))
        sys.exit(1)


def cmd_search(args):
    """Search for messages."""
    try:
        results = search_messages(args.query, limit=args.limit)
        
        if not results:
            print(f"No messages found matching '{args.query}'")
            return
        
        print(colored(f"\nSearch results for '{args.query}':", Colors.BOLD, Colors.CYAN))
        print("-" * 70)
        
        for msg in results:
            date_str = format_date(msg['date'])
            chat = truncate(msg['chat_name'], 20)
            sender = "Me" if msg['is_from_me'] else truncate(str(msg['sender']), 15)
            text = truncate(msg['text'], 40)
            
            print(f"{date_str:<20} {colored(chat, Colors.CYAN):<22} "
                  f"{colored(sender, Colors.YELLOW):<17} {text}")
        
        print(f"\nFound {len(results)} message(s)")
        
    except Exception as e:
        print(colored(f"Error searching: {e}", Colors.RED))
        sys.exit(1)


def cmd_chat(args):
    """Interactive chat mode with a contact."""
    try:
        conversations = get_conversations(limit=100)
        
        # Determine conversation
        chat_id = None
        chat_identifier = None
        chat_name = None
        
        if args.contact.isdigit():
            idx = int(args.contact) - 1
            if 0 <= idx < len(conversations):
                conv = conversations[idx]
                chat_id = conv['chat_id']
                chat_identifier = conv['chat_identifier']
                chat_name = conv['display_name']
            else:
                print(colored(f"Invalid conversation number", Colors.RED))
                sys.exit(1)
        else:
            chat_identifier = args.contact
            contact = get_contact_by_identifier(chat_identifier)
            if contact:
                chat_identifier = contact['chat_identifier'] or chat_identifier
                chat_name = contact['display_name'] or chat_identifier
            else:
                chat_name = chat_identifier
        
        print(colored(f"\n💬 Chat with {chat_name}", Colors.BOLD, Colors.CYAN))
        print(colored("Type your message and press Enter to send. Type 'quit' or Ctrl+C to exit.", Colors.DIM))
        print(colored("Type 'refresh' or 'r' to reload messages.", Colors.DIM))
        print("-" * 60)
        
        # Show recent messages
        def show_messages():
            if chat_id:
                messages = get_messages(chat_id=chat_id, limit=10)
            else:
                messages = get_messages(chat_identifier=chat_identifier, limit=10)
            
            for msg in messages:
                date_str = format_date(msg['date'])
                if msg['is_from_me']:
                    print(colored(f"  [{date_str}] Me: ", Colors.GREEN) + (msg['text'] or ""))
                else:
                    print(colored(f"  [{date_str}] {msg['sender']}: ", Colors.BLUE) + (msg['text'] or ""))
            print()
        
        show_messages()
        
        # Interactive loop
        while True:
            try:
                user_input = input(colored("You: ", Colors.GREEN, Colors.BOLD))
                
                if user_input.lower() in ('quit', 'exit', 'q'):
                    print("Goodbye!")
                    break
                
                if user_input.lower() in ('refresh', 'r'):
                    print(colored("\n--- Refreshing ---\n", Colors.DIM))
                    show_messages()
                    continue
                
                if not user_input.strip():
                    continue
                
                # Send the message
                success = send_message(chat_identifier, user_input)
                
                if success:
                    print(colored("  ✓ Sent", Colors.DIM))
                else:
                    print(colored("  ✗ Failed to send", Colors.RED))
                    
            except KeyboardInterrupt:
                print("\nGoodbye!")
                break
            except EOFError:
                print("\nGoodbye!")
                break
                
    except Exception as e:
        print(colored(f"Error: {e}", Colors.RED))
        sys.exit(1)


def cmd_status(args):
    """Show status information."""
    print(colored("\n📊 iMessage CLI Status", Colors.BOLD, Colors.CYAN))
    print("-" * 40)
    
    # Check database access
    try:
        from .database import get_db_path
        db_path = get_db_path()
        if db_path.exists():
            print(colored("✓ ", Colors.GREEN) + f"Database found: {db_path}")
        else:
            print(colored("✗ ", Colors.RED) + f"Database not found: {db_path}")
    except Exception as e:
        print(colored("✗ ", Colors.RED) + f"Database error: {e}")
    
    # Check Messages app
    try:
        running = check_messages_running()
        if running:
            print(colored("✓ ", Colors.GREEN) + "Messages app is running")
        else:
            print(colored("○ ", Colors.YELLOW) + "Messages app is not running")
    except Exception:
        print(colored("? ", Colors.YELLOW) + "Cannot check Messages app status")
    
    # Show stats
    try:
        conversations = get_conversations(limit=1000)
        unread = get_unread_count()
        print(f"\n📈 Statistics:")
        print(f"   Conversations: {len(conversations)}")
        print(f"   Unread messages: {unread}")
    except Exception:
        pass
    
    print()


def cmd_tui(args):
    """Launch the TUI interface."""
    try:
        run_tui()
    except KeyboardInterrupt:
        pass
    except Exception as e:
        print(colored(f"Error launching TUI: {e}", Colors.RED))
        sys.exit(1)


def main():
    """Main entry point for the CLI."""
    parser = argparse.ArgumentParser(
        prog='imessage',
        description='Read and respond to iMessages from the command line',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  imessage tui                     Launch interactive TUI with live updates
  imessage list                    List recent conversations
  imessage read 1                  Read messages from conversation #1
  imessage read "+1234567890"      Read messages from a phone number
  imessage send "+1234567890" "Hi" Send a message
  imessage chat 1                  Start interactive chat with conversation #1
  imessage search "meeting"        Search for messages containing "meeting"

Note: This tool requires macOS with Messages configured and proper permissions.
        """
    )
    
    parser.add_argument(
        '-v', '--version',
        action='version',
        version=f'%(prog)s {__version__}'
    )
    
    subparsers = parser.add_subparsers(dest='command', help='Available commands')
    
    # List command
    list_parser = subparsers.add_parser('list', aliases=['ls', 'l'], 
                                        help='List recent conversations')
    list_parser.add_argument('-n', '--limit', type=int, default=20,
                            help='Number of conversations to show (default: 20)')
    list_parser.set_defaults(func=cmd_list)
    
    # Read command
    read_parser = subparsers.add_parser('read', aliases=['r', 'view'],
                                        help='Read messages from a conversation')
    read_parser.add_argument('conversation',
                            help='Conversation number from list, or phone/email')
    read_parser.add_argument('-n', '--limit', type=int, default=30,
                            help='Number of messages to show (default: 30)')
    read_parser.set_defaults(func=cmd_read)
    
    # Send command
    send_parser = subparsers.add_parser('send', aliases=['s'],
                                        help='Send a message')
    send_parser.add_argument('recipient',
                            help='Phone number or email of recipient')
    send_parser.add_argument('message',
                            help='Message to send')
    send_parser.add_argument('-y', '--yes', action='store_true',
                            help='Skip confirmation prompt')
    send_parser.set_defaults(func=cmd_send)
    
    # Chat command (interactive)
    chat_parser = subparsers.add_parser('chat', aliases=['c'],
                                        help='Interactive chat mode')
    chat_parser.add_argument('contact',
                            help='Conversation number or phone/email')
    chat_parser.set_defaults(func=cmd_chat)
    
    # Search command
    search_parser = subparsers.add_parser('search', aliases=['find', 'grep'],
                                          help='Search messages')
    search_parser.add_argument('query',
                              help='Text to search for')
    search_parser.add_argument('-n', '--limit', type=int, default=20,
                              help='Maximum results (default: 20)')
    search_parser.set_defaults(func=cmd_search)
    
    # Status command
    status_parser = subparsers.add_parser('status',
                                          help='Show status and statistics')
    status_parser.set_defaults(func=cmd_status)
    
    # TUI command
    tui_parser = subparsers.add_parser('tui', aliases=['ui', 'watch'],
                                       help='Launch interactive TUI with live updates')
    tui_parser.set_defaults(func=cmd_tui)
    
    # Parse and execute
    args = parser.parse_args()
    
    if args.command is None:
        # Default to list if no command given
        args.limit = 20
        cmd_list(args)
    else:
        args.func(args)


if __name__ == '__main__':
    main()

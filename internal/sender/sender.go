// Package sender provides functionality for sending iMessages using AppleScript.
package sender

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/danewalton/imessage-cli/internal/database"
)

// SendMessage sends an iMessage to a recipient.
func SendMessage(recipient, message string) error {
	if strings.TrimSpace(recipient) == "" {
		return fmt.Errorf("recipient must not be empty")
	}
	escapedMessage := escapeForAppleScript(message)
	escapedRecipient := escapeForAppleScript(recipient)

	applescript := fmt.Sprintf(`
		tell application "Messages"
			set targetService to 1st service whose service type = iMessage
			set targetBuddy to buddy "%s" of targetService
			send "%s" to targetBuddy
		end tell
	`, escapedRecipient, escapedMessage)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", applescript)
	output, err := cmd.CombinedOutput()

	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("send outcome unknown: %w; check Messages before retrying", ctx.Err())
		}
		return sendNewMessage(recipient, message)
	}

	_ = output
	return nil
}

// SendToConversation addresses an existing chat by its Messages GUID.
func SendToConversation(conv database.Conversation, message string) error {
	if conv.ChatGUID == "" {
		return fmt.Errorf("conversation chat:%d has no Messages chat ID; cannot safely send", conv.ChatID)
	}
	return SendToChat(conv.ChatGUID, message)
}

// SendToChat sends to an exact Messages chat ID, never a name or participant.
func SendToChat(chatGUID, message string) error {
	if strings.TrimSpace(chatGUID) == "" {
		return fmt.Errorf("Messages chat ID must not be empty")
	}
	script := fmt.Sprintf(`
		tell application "Messages"
			set targetChat to chat id "%s"
			send "%s" to targetChat
		end tell
	`, escapeForAppleScript(chatGUID), escapeForAppleScript(message))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to send to chat: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// sendNewMessage sends a message by creating a new conversation.
func sendNewMessage(recipient, message string) error {
	escapedMessage := escapeForAppleScript(message)
	escapedRecipient := escapeForAppleScript(recipient)

	applescript := fmt.Sprintf(`
		tell application "Messages"
			set theBuddy to "%s"
			set theMessage to "%s"
			set theService to 1st account whose service type = iMessage
			set theParticipant to participant theBuddy of theService
			send theMessage to theParticipant
		end tell
	`, escapedRecipient, escapedMessage)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", applescript)
	output, err := cmd.CombinedOutput()

	if err != nil {
		return fmt.Errorf("failed to send message: %w: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

// SendToGroup sends a message to a group chat by name.
func SendToGroup(chatName, message string) error {
	escapedMessage := escapeForAppleScript(message)
	escapedName := escapeForAppleScript(chatName)

	applescript := fmt.Sprintf(`
		tell application "Messages"
			set theChat to 1st chat whose name = "%s"
			send "%s" to theChat
		end tell
	`, escapedName, escapedMessage)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", applescript)
	output, err := cmd.CombinedOutput()

	if err != nil {
		return fmt.Errorf("failed to send to group: %s", string(output))
	}

	return nil
}

// CheckMessagesRunning checks if the Messages app is running.
func CheckMessagesRunning() bool {
	applescript := `
		tell application "System Events"
			return (name of processes) contains "Messages"
		end tell
	`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", applescript)
	output, err := cmd.Output()

	if err != nil {
		return false
	}

	return strings.TrimSpace(strings.ToLower(string(output))) == "true"
}

// StartMessagesApp starts the Messages app if it's not running.
func StartMessagesApp() bool {
	applescript := `
		tell application "Messages"
			activate
		end tell
	`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", applescript)
	err := cmd.Run()

	return err == nil
}

func escapeForAppleScript(s string) string {
	// Escape backslashes first (order matters)
	s = strings.ReplaceAll(s, "\\", "\\\\")
	// Escape double quotes
	s = strings.ReplaceAll(s, "\"", "\\\"")
	// Replace newlines with AppleScript line breaks
	s = strings.ReplaceAll(s, "\n", "\" & return & \"")
	// Replace carriage returns
	s = strings.ReplaceAll(s, "\r", "")
	// Replace tabs with spaces
	s = strings.ReplaceAll(s, "\t", "    ")
	return s
}

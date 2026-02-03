// Package sender provides functionality for sending iMessages using AppleScript.
package sender

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SendMessage sends an iMessage to a recipient.
func SendMessage(recipient, message string) error {
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
		// Try alternative method
		return sendMessageAlternative(recipient, message)
	}

	_ = output
	return nil
}

// sendMessageAlternative is an alternative method to send message using chat specifier.
func sendMessageAlternative(recipient, message string) error {
	escapedMessage := escapeForAppleScript(message)
	escapedRecipient := escapeForAppleScript(recipient)

	applescript := fmt.Sprintf(`
		tell application "Messages"
			send "%s" to participant "%s" of (1st chat whose participants contains participant "%s")
		end tell
	`, escapedMessage, escapedRecipient, escapedRecipient)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", applescript)
	_, err := cmd.CombinedOutput()

	if err != nil {
		return sendNewMessage(recipient, message)
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
		return fmt.Errorf("failed to send message: %s", string(output))
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
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

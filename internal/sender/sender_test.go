package sender

import (
	"os"
	"strings"
	"testing"

	"github.com/danewalton/imessage-cli/internal/database"
	"github.com/danewalton/imessage-cli/internal/testutil"
)

func TestSelectedChatRouting(t *testing.T) {
	path := testutil.AppleScript(t)
	guid := `iMessage;+;chat-"synthetic"\group`
	message := "hello \"group\"\nsecond line"
	if err := SendToConversation(database.Conversation{ChatID: 42, ChatGUID: guid}, message); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if !strings.Contains(script, `chat id "`+escapeForAppleScript(guid)+`"`) ||
		!strings.Contains(script, escapeForAppleScript(message)) ||
		strings.Contains(script, "buddy ") || strings.Contains(script, "whose name") {
		t.Fatalf("incorrect selected-chat routing:\n%s", script)
	}
	t.Setenv("TEST_OSASCRIPT_FAIL", "1")
	err = SendToChat(guid, "failure")
	if err == nil || !strings.Contains(err.Error(), "Synthetic automation error") {
		t.Fatalf("missing failure details: %v", err)
	}
	data, _ = os.ReadFile(path)
	if strings.Count(string(data), "set targetChat") != 2 {
		t.Fatal("selected chat must not fall back to another destination")
	}
}

func TestMissingChatIdentityDoesNotSend(t *testing.T) {
	path := testutil.AppleScript(t)
	if err := SendToConversation(database.Conversation{ChatID: 42}, "private draft"); err == nil {
		t.Fatal("missing GUID accepted")
	}
	if err := SendToChat("", "private draft"); err == nil {
		t.Fatal("empty GUID accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("automation invoked without a safe target: %v", err)
	}
}

func TestDirectFallbackDoesNotChooseAContainingGroup(t *testing.T) {
	path := testutil.AppleScript(t)
	t.Setenv("TEST_OSASCRIPT_FAIL", "1")
	if err := SendMessage("new@example.test", "hello"); err == nil {
		t.Fatal("failure suppressed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `tell application "Messages"`) != 2 {
		t.Fatal("direct send and account fallback were not both exercised")
	}
	if strings.Contains(string(data), "1st chat") || strings.Contains(string(data), "participants contains") {
		t.Fatal("direct recipient fallback selected an arbitrary existing chat")
	}
}

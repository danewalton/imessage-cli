package tui

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danewalton/imessage-cli/internal/database"
	"github.com/danewalton/imessage-cli/internal/testutil"
	"github.com/danewalton/imessage-cli/internal/watcher"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type fakeWatcher struct {
	convs    []watcher.Conversation
	err      error
	mu       sync.Mutex
	messages []watcher.Message
}

func (w *fakeWatcher) GetConversations(int) ([]watcher.Conversation, error) { return w.convs, w.err }
func (w *fakeWatcher) GetMessages(id int64, _ int) ([]watcher.Message, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.messages != nil {
		return append([]watcher.Message(nil), w.messages...), w.err
	}
	return []watcher.Message{{MessageID: id, ChatID: id, Text: fmt.Sprintf("chat %d", id)}}, w.err
}
func (*fakeWatcher) OnNewMessages(watcher.MessageCallback)               {}
func (*fakeWatcher) OnConversationsUpdated(watcher.ConversationCallback) {}
func (*fakeWatcher) OnError(watcher.ErrorCallback)                       {}
func (*fakeWatcher) Start()                                              {}
func (*fakeWatcher) Stop()                                               {}

func testUI(t *testing.T) *MessagesTUI {
	t.Helper()
	ui := NewMessagesTUI()
	ui.watcher = &fakeWatcher{}
	ui.app = tview.NewApplication()
	ui.convList = tview.NewList()
	ui.msgView = tview.NewTextView().SetDynamicColors(true)
	ui.inputField = tview.NewInputField()
	ui.statusBar = tview.NewTextView().SetDynamicColors(true)
	ui.pages = tview.NewPages()
	ui.conversations = []watcher.Conversation{
		{ChatID: 1, ChatGUID: "iMessage;-;amber", DisplayName: "Amber"},
		{ChatID: 2, ChatGUID: "iMessage;-;blue", DisplayName: "Blue"},
	}
	ui.selectedChatID = 1
	ui.messages = []watcher.Message{{ChatID: 1, Text: "Amber's history"}}
	ui.applyConversations(ui.conversations)
	ui.renderMessages()
	ui.setupCallbacks()
	ui.app.SetScreen(tcell.NewSimulationScreen(""))
	ui.app.SetRoot(tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(ui.convList, 0, 1, false).
		AddItem(ui.msgView, 0, 2, false).
		AddItem(ui.inputField, 3, 0, false), true)
	done := make(chan error, 1)
	go func() { done <- ui.app.Run() }()
	ui.app.QueueUpdateDraw(func() {})
	t.Cleanup(func() {
		ui.app.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("UI failed to stop")
		}
	})
	return ui
}

func waitUI(t *testing.T, ui *MessagesTUI, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var ready bool
		ui.app.QueueUpdateDraw(func() { ready = predicate() })
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("UI condition timed out")
}

func TestConversationReorderKeepsRecipientAndDraft(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() {
		ui.inputField.SetText("draft for Amber")
	})
	ui.app.QueueUpdateDraw(func() {
		ui.applyConversations([]watcher.Conversation{ui.conversations[1], ui.conversations[0]})
		if ui.selectedChatID != 1 || ui.convList.GetCurrentItem() != 1 || ui.inputField.GetText() != "draft for Amber" {
			t.Errorf("recipient/draft changed: %d,%d,%q", ui.selectedChatID, ui.convList.GetCurrentItem(), ui.inputField.GetText())
		}
	})
}

func TestDraftsStayWithTheirConversation(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("Amber draft") })
	ui.app.QueueUpdateDraw(func() { ui.selectConversation(1) })
	waitUI(t, ui, func() bool { return len(ui.messages) > 0 && ui.messages[0].ChatID == 2 })
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("Blue draft") })
	ui.app.QueueUpdateDraw(func() {
		ui.selectConversation(0)
		if ui.inputField.GetText() != "Amber draft" {
			t.Error("Amber draft lost")
		}
	})
	waitUI(t, ui, func() bool { return len(ui.messages) > 0 && ui.messages[0].ChatID == 1 })
	ui.app.QueueUpdateDraw(func() {
		ui.selectConversation(1)
		if ui.inputField.GetText() != "Blue draft" {
			t.Error("Blue draft lost")
		}
	})
	waitUI(t, ui, func() bool { return len(ui.messages) > 0 && ui.messages[0].ChatID == 2 })
}

func TestStaleLoadsAndFailuresPreserveCurrentMessages(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() {
		ui.selectedChatID = 2
		ui.loadGeneration = 10
		ui.messages = []watcher.Message{{ChatID: 2, Text: "Blue history"}}
		ui.renderMessages()
		ui.applyMessages(1, 10, []watcher.Message{{Text: "stale Amber"}}, nil)
		ui.applyMessages(2, 9, []watcher.Message{{Text: "stale Blue"}}, nil)
		ui.applyMessages(2, 10, nil, errors.New("database denied"))
		if ui.selectedChatID != 2 || ui.messages[0].Text != "Blue history" || !strings.Contains(ui.msgView.GetText(true), "Blue history") {
			t.Error("stale or failed load overwrote current state")
		}
		if !strings.Contains(ui.statusBar.GetText(true), "database denied") {
			t.Error("load error hidden")
		}
		ui.applyMessages(2, 10, nil, nil)
		if !strings.Contains(ui.msgView.GetText(true), "No messages") {
			t.Error("successful empty result treated as an error")
		}
	})
}

func TestRejectedSendsRemainResponsiveAndKeepDraft(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("keep this draft") })
	done := make(chan struct{})
	go func() {
		ui.app.QueueUpdateDraw(func() {
			enter := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)
			handler := ui.inputField.InputHandler()
			focus := func(p tview.Primitive) { ui.app.SetFocus(p) }
			ui.sendingMessage.Store(true)
			handler(enter, focus)
			if ui.inputField.GetText() != "keep this draft" {
				t.Error("rejected draft cleared")
			}
			ui.sendingMessage.Store(false)
			ui.selectedChatID = 0
			handler(enter, focus)
			if ui.inputField.GetText() != "keep this draft" {
				t.Error("no-selection draft cleared")
			}
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send rejection deadlocked the event loop")
	}
}

func TestFailedSendKeepsNewDraftAndCanBeRecovered(t *testing.T) {
	ui := testUI(t)
	release := make(chan struct{})
	ui.app.QueueUpdateDraw(func() {
		ui.send = func(watcher.Conversation, string) error {
			<-release
			return errors.New("synthetic send failure")
		}
		ui.inputField.SetText("first draft")
	})
	ui.app.QueueUpdateDraw(func() { ui.sendMessage(ui.inputField.GetText()) })
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("second draft") })
	close(release)
	waitUI(t, ui, func() bool { return !ui.sendingMessage.Load() })
	ui.app.QueueUpdateDraw(func() {
		if ui.inputField.GetText() != "second draft" || len(ui.failedDrafts[1]) != 1 || ui.failedDrafts[1][0] != "first draft" {
			t.Error("failed send lost or replaced a draft")
		}
		ui.recoverFailedDraft()
		if ui.inputField.GetText() != "second draft" {
			t.Error("recovery replaced current draft")
		}
		ui.inputField.SetText("")
	})
	ui.app.QueueUpdateDraw(func() {
		ui.recoverFailedDraft()
		if ui.inputField.GetText() != "first draft" || len(ui.failedDrafts[1]) != 0 {
			t.Error("failed draft not recoverable")
		}
	})
}

func TestFailureAfterChatSwitchIsStoredForOriginalChat(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("Blue draft") })
	ui.app.QueueUpdateDraw(func() {
		ui.selectedChatID = 2
		ui.finishSend(1, "Amber", "failed Amber message", ui.draftVersion, errors.New("failed"))
		if ui.inputField.GetText() != "Blue draft" || len(ui.failedDrafts[1]) != 1 || len(ui.failedDrafts[2]) != 0 {
			t.Error("failed text was restored to a different chat")
		}
	})
}

func TestFailureRestoresOnlyAnUntouchedComposer(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() {
		ui.finishSend(1, "Amber", "first failed draft", ui.draftVersion, errors.New("failed"))
		if ui.inputField.GetText() != "first failed draft" || len(ui.failedDrafts[1]) != 0 {
			t.Error("untouched composer did not restore its failed message")
		}
	})
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("") })
	var version uint64
	ui.app.QueueUpdateDraw(func() { version = ui.draftVersion; ui.inputField.SetText("typed later") })
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("") })
	ui.app.QueueUpdateDraw(func() {
		ui.finishSend(1, "Amber", "second failed draft", version, errors.New("failed"))
		if ui.inputField.GetText() != "" || len(ui.failedDrafts[1]) != 1 {
			t.Error("edited-and-cleared composer was replaced")
		}
	})
}

func TestDefaultTUISendsToExactGroup(t *testing.T) {
	path := testutil.AppleScript(t)
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() {
		ui.applyConversations([]watcher.Conversation{
			{ChatID: 3, ChatGUID: "iMessage;+;synthetic-group", DisplayName: "Group", Participants: []string{"amber", "blue"}},
		})
	})
	waitUI(t, ui, func() bool { return len(ui.messages) > 0 && ui.messages[0].ChatID == 3 })
	ui.app.QueueUpdateDraw(func() { ui.inputField.SetText("group reply") })
	ui.app.QueueUpdateDraw(func() { ui.sendMessage(ui.inputField.GetText()) })
	waitUI(t, ui, func() bool { return !ui.sendingMessage.Load() })
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `chat id "iMessage;+;synthetic-group"`) || strings.Contains(string(data), "buddy ") {
		t.Fatalf("TUI group routing: %v\n%s", err, data)
	}
	time.Sleep(MessageRefreshDelay + 50*time.Millisecond)
}

func TestFailedRefreshPreservesLoadedDataAndDraft(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() {
		ui.watcher = &fakeWatcher{err: errors.New("database busy")}
		ui.inputField.SetText("keep draft")
	})
	ui.app.QueueUpdateDraw(func() { ui.refresh() })
	waitUI(t, ui, func() bool { return !ui.refreshing.Load() })
	ui.app.QueueUpdateDraw(func() {
		if ui.selectedChatID != 1 || len(ui.conversations) != 2 || ui.messages[0].Text != "Amber's history" || ui.inputField.GetText() != "keep draft" {
			t.Error("failed refresh cleared existing state")
		}
		if !strings.Contains(ui.statusBar.GetText(true), "database busy") {
			t.Error("refresh failure hidden")
		}
	})
}

func TestSuccessfulSendRefreshDoesNotReopenOldChat(t *testing.T) {
	ui := testUI(t)
	release := make(chan struct{})
	ui.app.QueueUpdateDraw(func() {
		ui.send = func(conv watcher.Conversation, text string) error {
			if conv.ChatID != 1 || conv.ChatGUID != "iMessage;-;amber" {
				return errors.New("wrong recipient")
			}
			<-release
			return nil
		}
		ui.inputField.SetText("Amber message")
	})
	ui.app.QueueUpdateDraw(func() { ui.sendMessage(ui.inputField.GetText()) })
	ui.app.QueueUpdateDraw(func() { ui.selectConversation(1) })
	waitUI(t, ui, func() bool { return len(ui.messages) > 0 && ui.messages[0].ChatID == 2 })
	close(release)
	waitUI(t, ui, func() bool { return !ui.sendingMessage.Load() })
	time.Sleep(MessageRefreshDelay + 50*time.Millisecond)
	ui.app.QueueUpdateDraw(func() {
		if ui.selectedChatID != 2 || ui.messages[0].ChatID != 2 || !strings.Contains(ui.msgView.GetTitle(), "Blue") {
			t.Error("post-send refresh reopened the old conversation")
		}
	})
}

func TestExternalTextIsLiteral(t *testing.T) {
	ui := testUI(t)
	ui.app.QueueUpdateDraw(func() {
		var text strings.Builder
		ui.formatMessageLine(&text, watcher.Message{
			Text: "literal [red] token [-]", Sender: "[blue]name",
			Attachments: []watcher.Attachment{{Filename: "[green]file.png", IsImage: true}},
		})
		ui.msgView.SetText(text.String())
		plain := ui.msgView.GetText(true)
		for _, literal := range []string{"[red]", "[-]", "[blue]", "[green]"} {
			if !strings.Contains(plain, literal) {
				t.Errorf("lost literal %q: %q", literal, plain)
			}

		}
		ui.setStatus("database error [red]")
		if !strings.Contains(ui.statusBar.GetText(true), "[red]") {
			t.Error("status interpreted external markup")
		}
	})
}

func TestReactionBadgesAreLiteralAndPartAware(t *testing.T) {
	var text strings.Builder
	ui := NewMessagesTUI()
	ui.formatMessageLine(&text, watcher.Message{
		Text: "original message", Sender: "Amber",
		Attachments: []watcher.Attachment{{Filename: "one.png"}, {Filename: "two.png"}},
		Reactions: []database.Reaction{
			{Type: database.ReactionLike, Sender: "Blue", Part: 0},
			{Type: database.ReactionEmoji, Emoji: "[red]", Sender: "[blue]Me", Part: 1},
			{Type: database.ReactionSticker, Sender: "Amber", Part: -1},
		},
	})
	view := tview.NewTextView().SetDynamicColors(true).SetText(text.String())
	plain := view.GetText(true)
	for _, want := range []string{
		"original message", "👍 Blue (part 1)", "[red] [blue]Me (part 2)", "Sticker Amber (bubble)",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing literal badge %q: %q", want, plain)
		}
	}
	text.Reset()
	ui.formatMessageLine(&text, watcher.Message{IsReaction: true, Text: "[Attachment]"})
	if text.Len() != 0 {
		t.Error("reaction event rendered as a standalone message")
	}
}

func TestLiveReactionsPreserveComposerAndScroll(t *testing.T) {
	ui := testUI(t)
	message := watcher.Message{MessageID: 1, GUID: "message-1", ChatID: 1, Text: strings.Repeat("history line\n", 90)}
	w := &fakeWatcher{messages: []watcher.Message{message}}
	var convs []watcher.Conversation
	ui.app.QueueUpdateDraw(func() {
		ui.watcher = w
		ui.messages = []watcher.Message{message}
		ui.msgView.SetScrollable(true).SetWrap(false)
		ui.renderMessages()
		ui.inputField.SetText("keep this draft")
		ui.app.SetFocus(ui.inputField)
		convs = append([]watcher.Conversation(nil), ui.conversations...)
	})
	ui.app.QueueUpdateDraw(func() { ui.msgView.ScrollTo(7, 0) })
	var row, column int
	ui.app.QueueUpdateDraw(func() { row, column = ui.msgView.GetScrollOffset() })
	if row != 7 {
		t.Fatalf("scroll fixture did not reach requested position: %d", row)
	}
	message.Reactions = []database.Reaction{{Type: database.ReactionLike, Sender: "Blue"}}
	w.mu.Lock()
	w.messages = []watcher.Message{message}
	w.mu.Unlock()
	ui.onNewMessages([]watcher.Message{{MessageID: 2, ChatID: 1, IsReaction: true, Sender: "Blue"}})
	waitUI(t, ui, func() bool { return strings.Contains(ui.msgView.GetText(true), "👍 Blue") })
	ui.app.QueueUpdateDraw(func() {
		gotRow, gotColumn := ui.msgView.GetScrollOffset()
		if gotRow != row || gotColumn != column || ui.inputField.GetText() != "keep this draft" || ui.app.GetFocus() != ui.inputField {
			t.Error("reaction update moved the scroll or disturbed the composer")
		}
		if !strings.Contains(ui.statusBar.GetText(true), "Reaction updated by Blue") {
			t.Error("reaction announced as a new message")
		}
	})
	message.Reactions = nil
	w.mu.Lock()
	w.messages = []watcher.Message{message}
	w.mu.Unlock()
	ui.onConversationsUpdated(convs)
	waitUI(t, ui, func() bool { return len(ui.messages) == 1 && len(ui.messages[0].Reactions) == 0 })
	ui.app.QueueUpdateDraw(func() {
		if strings.Contains(ui.msgView.GetText(true), "👍 Blue") || ui.inputField.GetText() != "keep this draft" {
			t.Error("deletion-only refresh retained a reaction or changed the draft")
		}
		gotRow, gotColumn := ui.msgView.GetScrollOffset()
		if gotRow != row || gotColumn != column {
			t.Error("reaction removal moved the scroll")
		}
	})
}
func TestDebugLoggingDoesNotCaptureComposer(t *testing.T) {
	ui := testUI(t)
	var buffer bytes.Buffer
	ui.app.QueueUpdateDraw(func() {
		ui.logger = log.New(&buffer, "", 0)
		ui.app.SetFocus(ui.inputField)
		for _, r := range "TEST-SECRET" {
			ui.app.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
		}
	})
	if strings.Contains(buffer.String(), "rune=") || strings.Contains(buffer.String(), "TEST-SECRET") {
		t.Fatal("composer characters leaked to diagnostics")
	}
}

func TestDebugLogIsPrivate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	f, err := openDebugLog("")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	for _, name := range []string{path, filepath.Dir(path)} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("not private: %s, %v", name, err)
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	f, err = openDebugLog(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := f.Stat()
	f.Close()
	if info.Mode().Perm() != 0600 {
		t.Error("reopening did not repair log permissions")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openDebugLog(link); err == nil {
		f.Close()
		t.Fatal("debug logger followed a symlink")
	}
	hardLink := filepath.Join(t.TempDir(), "hard-link")
	if err := os.Link(path, hardLink); err != nil {
		t.Fatal(err)
	}
	if f, err := openDebugLog(path); err == nil {
		f.Close()
		t.Fatal("debug logger accepted a multiply linked file")
	}
}

func TestInitialDatabaseFailureIsNotEmptyState(t *testing.T) {
	ui := NewMessagesTUI()
	ui.watcher = &fakeWatcher{err: errors.New("database access denied")}
	if err := ui.loadInitialData(); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("initial failure hidden: %v", err)
	}
}

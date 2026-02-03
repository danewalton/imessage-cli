// Package tui provides the text user interface for iMessage CLI.
package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/danewalton/imessage-cli/internal/sender"
	"github.com/danewalton/imessage-cli/internal/watcher"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// MessagesTUI is the main TUI application.
type MessagesTUI struct {
	app        *tview.Application
	pages      *tview.Pages
	convList   *tview.List
	msgView    *tview.TextView
	inputField *tview.InputField
	statusBar  *tview.TextView
	mainFlex   *tview.Flex

	watcher         *watcher.MessageWatcher
	conversations   []watcher.Conversation
	messages        []watcher.Message
	selectedChatID  int64
	selectedChatIdx int

	mu sync.RWMutex
}

// NewMessagesTUI creates a new TUI instance.
func NewMessagesTUI() *MessagesTUI {
	return &MessagesTUI{
		watcher: watcher.NewMessageWatcher(500 * time.Millisecond),
	}
}

// Run starts the TUI application.
func Run() error {
	tui := NewMessagesTUI()
	return tui.run()
}

func (t *MessagesTUI) run() error {
	t.app = tview.NewApplication()

	// Create conversation list
	t.convList = tview.NewList().
		ShowSecondaryText(true).
		SetHighlightFullLine(true).
		SetSelectedBackgroundColor(tcell.ColorDarkCyan).
		SetSelectedTextColor(tcell.ColorWhite)
	t.convList.SetBorder(true).SetTitle(" Conversations ")

	// Create message view
	t.msgView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetWordWrap(true)
	t.msgView.SetBorder(true).SetTitle(" Messages ")

	// Create input field
	t.inputField = tview.NewInputField().
		SetLabel("Send: ").
		SetLabelColor(tcell.ColorGreen).
		SetFieldBackgroundColor(tcell.ColorBlack)
	t.inputField.SetBorder(true)

	// Create status bar
	t.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	t.statusBar.SetBackgroundColor(tcell.ColorDarkGreen)
	t.setStatus("↑↓:Nav  Enter:Select  Tab:Switch  i:Input  r:Refresh  q:Quit")

	// Layout
	rightPanel := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.msgView, 0, 1, false).
		AddItem(t.inputField, 3, 0, false)

	t.mainFlex = tview.NewFlex().
		AddItem(t.convList, 35, 0, true).
		AddItem(rightPanel, 0, 1, false)

	mainLayout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.mainFlex, 0, 1, true).
		AddItem(t.statusBar, 1, 0, false)

	t.pages = tview.NewPages().
		AddPage("main", mainLayout, true, true)

	// Setup callbacks
	t.setupCallbacks()

	// Setup watcher
	t.watcher.OnNewMessages(t.onNewMessages)
	t.watcher.OnConversationsUpdated(t.onConversationsUpdated)

	// Load initial data synchronously (before app.Run)
	t.loadInitialData()

	// Start watcher after initial load
	t.watcher.Start()
	defer t.watcher.Stop()

	// Run the application
	return t.app.SetRoot(t.pages, true).EnableMouse(true).Run()
}

func (t *MessagesTUI) setupCallbacks() {
	// Conversation selection
	t.convList.SetChangedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		t.selectedChatIdx = index
		t.mu.RLock()
		if index >= 0 && index < len(t.conversations) {
			conv := t.conversations[index]
			t.selectedChatID = conv.ChatID
			t.mu.RUnlock()
			t.loadMessages(conv.ChatID)
		} else {
			t.mu.RUnlock()
		}
	})

	t.convList.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		t.app.SetFocus(t.msgView)
		t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  r:Refresh  q:Quit")
	})

	// Input handling
	t.inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			text := t.inputField.GetText()
			if text != "" {
				t.sendMessage(text)
				t.inputField.SetText("")
			}
			t.app.SetFocus(t.msgView)
		} else if key == tcell.KeyEscape {
			t.app.SetFocus(t.msgView)
		}
	})

	// Global key handling
	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		focused := t.app.GetFocus()

		// Handle input field separately
		if focused == t.inputField {
			return event
		}

		switch event.Key() {
		case tcell.KeyTab:
			if focused == t.convList {
				t.app.SetFocus(t.msgView)
				t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  r:Refresh  q:Quit")
			} else {
				t.app.SetFocus(t.convList)
				t.setStatus("[CONV] ↑↓:Nav  Enter:Select  Tab:Switch  i:Input  r:Refresh  q:Quit")
			}
			return nil

		case tcell.KeyRune:
			switch event.Rune() {
			case 'q', 'Q':
				t.app.Stop()
				return nil
			case 'i':
				t.app.SetFocus(t.inputField)
				t.setStatus("[INPUT] Enter:Send  Esc:Cancel")
				return nil
			case 'r', 'R':
				t.refresh()
				return nil
			case 'h':
				if focused == t.msgView {
					t.app.SetFocus(t.convList)
					t.setStatus("[CONV] ↑↓:Nav  Enter:Select  Tab:Switch  i:Input  r:Refresh  q:Quit")
					return nil
				}
			case 'l':
				if focused == t.convList {
					t.app.SetFocus(t.msgView)
					t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  r:Refresh  q:Quit")
					return nil
				}
			case 'j':
				if focused == t.msgView {
					row, col := t.msgView.GetScrollOffset()
					t.msgView.ScrollTo(row+1, col)
					return nil
				}
			case 'k':
				if focused == t.msgView {
					row, col := t.msgView.GetScrollOffset()
					if row > 0 {
						t.msgView.ScrollTo(row-1, col)
					}
					return nil
				}
			case 'g':
				if focused == t.msgView {
					t.msgView.ScrollToBeginning()
					return nil
				}
			case 'G':
				if focused == t.msgView {
					t.msgView.ScrollToEnd()
					return nil
				}
			}

		case tcell.KeyLeft:
			if focused == t.msgView {
				t.app.SetFocus(t.convList)
				t.setStatus("[CONV] ↑↓:Nav  Enter:Select  Tab:Switch  i:Input  r:Refresh  q:Quit")
				return nil
			}
		case tcell.KeyRight:
			if focused == t.convList {
				t.app.SetFocus(t.msgView)
				t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  r:Refresh  q:Quit")
				return nil
			}
		}

		return event
	})
}

func (t *MessagesTUI) setStatus(msg string) {
	t.statusBar.SetText(" " + msg + " ")
}

// loadInitialData loads data synchronously before the app starts
func (t *MessagesTUI) loadInitialData() {
	convs := t.watcher.GetConversations(50)

	t.mu.Lock()
	t.conversations = convs
	t.mu.Unlock()

	// Populate UI directly (no QueueUpdateDraw needed before Run())
	t.convList.Clear()
	for _, conv := range convs {
		name := conv.DisplayName
		if len(name) > 30 {
			name = name[:27] + "..."
		}

		secondary := t.formatTime(conv.LastMessageDate)
		if conv.UnreadCount > 0 {
			name = fmt.Sprintf("(%d) %s", conv.UnreadCount, name)
		}

		t.convList.AddItem(name, secondary, 0, nil)
	}

	// Load first conversation's messages
	if len(convs) > 0 {
		t.selectedChatID = convs[0].ChatID
		msgs := t.watcher.GetMessages(convs[0].ChatID, 100)

		t.mu.Lock()
		t.messages = msgs
		t.mu.Unlock()

		t.msgView.SetTitle(fmt.Sprintf(" %s ", convs[0].DisplayName))

		var builder strings.Builder
		for _, msg := range msgs {
			timeStr := t.formatTime(msg.Date)
			if msg.IsFromMe {
				builder.WriteString(fmt.Sprintf("[green][%s] Me:[-] %s\n", timeStr, msg.Text))
			} else {
				sender := msg.Sender
				if len(sender) > 15 {
					sender = sender[:12] + "..."
				}
				builder.WriteString(fmt.Sprintf("[cyan][%s] %s:[-] %s\n", timeStr, sender, msg.Text))
			}
		}
		t.msgView.SetText(builder.String())
	}
}

func (t *MessagesTUI) loadConversations() {
	convs := t.watcher.GetConversations(50)

	t.mu.Lock()
	t.conversations = convs
	t.mu.Unlock()

	t.app.QueueUpdateDraw(func() {
		t.convList.Clear()
		for _, conv := range convs {
			name := conv.DisplayName
			if len(name) > 30 {
				name = name[:27] + "..."
			}

			secondary := t.formatTime(conv.LastMessageDate)
			if conv.UnreadCount > 0 {
				name = fmt.Sprintf("(%d) %s", conv.UnreadCount, name)
			}

			t.convList.AddItem(name, secondary, 0, nil)
		}

		if len(convs) > 0 && t.selectedChatID == 0 {
			t.selectedChatID = convs[0].ChatID
			t.loadMessages(convs[0].ChatID)
		}
	})
}

func (t *MessagesTUI) loadMessages(chatID int64) {
	msgs := t.watcher.GetMessages(chatID, 100)

	t.mu.Lock()
	t.messages = msgs
	t.selectedChatID = chatID
	t.mu.Unlock()

	// Find conversation name
	var chatName string
	t.mu.RLock()
	for _, conv := range t.conversations {
		if conv.ChatID == chatID {
			chatName = conv.DisplayName
			break
		}
	}
	t.mu.RUnlock()

	t.app.QueueUpdateDraw(func() {
		t.msgView.Clear()
		t.msgView.SetTitle(fmt.Sprintf(" %s ", chatName))

		var builder strings.Builder
		for _, msg := range msgs {
			timeStr := t.formatTime(msg.Date)

			if msg.IsFromMe {
				builder.WriteString(fmt.Sprintf("[green][%s] Me:[-] %s\n", timeStr, msg.Text))
			} else {
				sender := msg.Sender
				if len(sender) > 15 {
					sender = sender[:12] + "..."
				}
				builder.WriteString(fmt.Sprintf("[cyan][%s] %s:[-] %s\n", timeStr, sender, msg.Text))
			}
		}
		t.msgView.SetText(builder.String())
		t.msgView.ScrollToEnd()
	})
}

func (t *MessagesTUI) sendMessage(text string) {
	t.mu.RLock()
	chatID := t.selectedChatID
	var chatIdent string
	for _, conv := range t.conversations {
		if conv.ChatID == chatID {
			chatIdent = conv.ChatIdentifier
			break
		}
	}
	t.mu.RUnlock()

	if chatIdent == "" {
		t.setStatus("Error: No conversation selected")
		return
	}

	err := sender.SendMessage(chatIdent, text)
	if err != nil {
		t.setStatus(fmt.Sprintf("Error: %v", err))
	} else {
		t.setStatus("✓ Message sent!")
		// Refresh messages after a short delay
		go func() {
			time.Sleep(500 * time.Millisecond)
			t.loadMessages(chatID)
		}()
	}
}

func (t *MessagesTUI) refresh() {
	t.setStatus("🔄 Refreshing...")
	t.loadConversations()

	t.mu.RLock()
	chatID := t.selectedChatID
	t.mu.RUnlock()

	if chatID > 0 {
		t.loadMessages(chatID)
	}
	t.setStatus("✓ Refreshed!")
}

func (t *MessagesTUI) onNewMessages(msgs []watcher.Message) {
	t.mu.RLock()
	currentChatID := t.selectedChatID
	t.mu.RUnlock()

	// Check if any messages are for the current chat
	for _, msg := range msgs {
		if msg.ChatID == currentChatID {
			t.loadMessages(currentChatID)
			break
		}
	}

	// Show notification for incoming messages
	if len(msgs) > 0 && !msgs[len(msgs)-1].IsFromMe {
		t.app.QueueUpdateDraw(func() {
			t.setStatus(fmt.Sprintf("📬 New message from %s", msgs[len(msgs)-1].Sender))
		})
	}
}

func (t *MessagesTUI) onConversationsUpdated(convs []watcher.Conversation) {
	t.mu.Lock()
	t.conversations = convs
	t.mu.Unlock()

	t.app.QueueUpdateDraw(func() {
		// Preserve selection
		selectedIdx := t.convList.GetCurrentItem()

		t.convList.Clear()
		for _, conv := range convs {
			name := conv.DisplayName
			if len(name) > 30 {
				name = name[:27] + "..."
			}

			secondary := t.formatTime(conv.LastMessageDate)
			if conv.UnreadCount > 0 {
				name = fmt.Sprintf("(%d) %s", conv.UnreadCount, name)
			}

			t.convList.AddItem(name, secondary, 0, nil)
		}

		if selectedIdx >= 0 && selectedIdx < len(convs) {
			t.convList.SetCurrentItem(selectedIdx)
		}
	})
}

func (t *MessagesTUI) formatTime(tm *time.Time) string {
	if tm == nil {
		return ""
	}

	now := time.Now()
	diff := now.Sub(*tm)

	if diff.Hours() < 24 {
		return tm.Format("15:04")
	} else if diff.Hours() < 48 {
		return "Yesterday"
	} else if diff.Hours() < 168 {
		return tm.Format("Mon")
	}
	return tm.Format("01/02")
}

// Package tui provides the text user interface for iMessage CLI.
package tui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/danewalton/imessage-cli/internal/sender"
	"github.com/danewalton/imessage-cli/internal/watcher"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// UI constants
const (
	DefaultConversationLimit = 50
	DefaultMessageLimit      = 100
	MaxDisplayNameLength     = 30
	MaxSenderNameLength      = 15
	MessageRefreshDelay      = 500 * time.Millisecond
	LockFileName             = ".imessage-tui.lock"
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
	// sendingMessage tracks whether a message send is in progress
	sendingMessage atomic.Bool
	// refreshing tracks whether a refresh is in progress
	refreshing atomic.Bool
	// logging
	logger  *log.Logger
	logFile *os.File
	debug   bool
}

// NewMessagesTUI creates a new TUI instance.
func NewMessagesTUI() *MessagesTUI {
	return &MessagesTUI{
		watcher: watcher.NewMessageWatcher(500 * time.Millisecond),
	}
}

// acquireLock attempts to acquire an exclusive lock to prevent multiple instances.
// Returns the lock file handle (caller must close it) or an error.
func acquireLock() (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot get home directory: %w", err)
	}

	lockPath := filepath.Join(home, LockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot open lock file: %w", err)
	}

	// Try to acquire an exclusive lock (non-blocking)
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("another instance of imessage-tui is already running (lock file: %s)", lockPath)
	}

	// Write PID to lock file for debugging
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Sync()

	return f, nil
}

// RunWithDebug runs the TUI with optional debug logging to the provided path.
func RunWithDebug(enable bool, logPath string) error {
	// Acquire lock to prevent multiple instances
	lockFile, err := acquireLock()
	if err != nil {
		return err
	}
	defer func() {
		syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
	}()

	t := NewMessagesTUI()
	t.debug = enable
	if enable {
		if logPath == "" {
			logPath = "/tmp/imessage-tui.log"
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("unable to open log file: %w", err)
		}
		t.logFile = f
		t.logger = log.New(f, "tui: ", log.LstdFlags|log.Lmicroseconds)
		t.logf("debug logging enabled, file=%s", logPath)
	}
	defer func() {
		if t.logFile != nil {
			t.logFile.Sync()
			t.logFile.Close()
		}
	}()

	return t.run()
}

// Run starts the TUI application.
func Run() error {
	// Acquire lock to prevent multiple instances
	lockFile, err := acquireLock()
	if err != nil {
		return err
	}
	defer func() {
		syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
	}()

	tui := NewMessagesTUI()
	return tui.run()
}

func (t *MessagesTUI) run() error {
	if t.logger != nil {
		t.logf("run: starting TUI run")
	}
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

	// Setup watcher
	t.watcher.OnNewMessages(t.onNewMessages)
	t.watcher.OnConversationsUpdated(t.onConversationsUpdated)

	// Load initial data synchronously (before app.Run)
	t.loadInitialData()

	// Register UI callbacks after initial population to avoid triggering them
	// while we're still populating the list (which can cause QueueUpdateDraw
	// to block if called before app.Run()).
	if t.logger != nil {
		t.logf("run: registering callbacks after initial load")
	}
	t.setupCallbacks()

	// Start watcher after initial load
	if t.logger != nil {
		t.logf("run: starting watcher")
	}
	t.watcher.Start()
	defer func() {
		if t.logger != nil {
			t.logf("run: stopping watcher")
		}
		t.watcher.Stop()
	}()

	// Run the application
	if t.logger != nil {
		t.logf("run: entering app.Run()")
	}
	err := t.app.SetRoot(t.pages, true).EnableMouse(true).Run()
	if err != nil && t.logger != nil {
		t.logf("run: app.Run error: %v", err)
	}
	return err
}

func (t *MessagesTUI) setupCallbacks() {
	if t.logger != nil {
		t.logf("setupCallbacks: registering callbacks")
	}
	// Conversation selection
	t.convList.SetChangedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		t.selectedChatIdx = index
		t.mu.RLock()
		if index >= 0 && index < len(t.conversations) {
			conv := t.conversations[index]
			t.selectedChatID = conv.ChatID
			t.mu.RUnlock()
			// Run in goroutine to avoid deadlock when called from within QueueUpdateDraw
			go t.loadMessages(conv.ChatID)
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
		if t.logger != nil {
			// Log basic input event info for debugging frozen UI issues
			var r rune
			if event != nil {
				r = event.Rune()
			}
			t.logf("input event: key=%v rune=%q focused=%T", event.Key(), r, focused)
		}

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

func (t *MessagesTUI) logf(format string, v ...interface{}) {
	if t.logger != nil {
		t.logger.Printf(format, v...)
	}
}

// loadInitialData loads data synchronously before the app starts
func (t *MessagesTUI) loadInitialData() {
	convs := t.watcher.GetConversations(DefaultConversationLimit)

	if t.logger != nil {
		t.logf("loadInitialData: got %d conversations", len(convs))
	}

	t.mu.Lock()
	t.conversations = convs
	t.mu.Unlock()

	// Populate UI directly (no QueueUpdateDraw needed before Run())
	t.convList.Clear()
	for _, conv := range convs {
		name := conv.DisplayName
		if len(name) > MaxDisplayNameLength {
			name = name[:MaxDisplayNameLength-3] + "..."
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
		msgs := t.watcher.GetMessages(convs[0].ChatID, DefaultMessageLimit)

		t.mu.Lock()
		t.messages = msgs
		t.mu.Unlock()

		t.msgView.SetTitle(fmt.Sprintf(" %s ", convs[0].DisplayName))

		if msgs == nil {
			t.msgView.SetText("[yellow]No messages or unable to load messages[-]")
		} else {
			var builder strings.Builder
			for _, msg := range msgs {
				timeStr := t.formatTime(msg.Date)
				if msg.IsFromMe {
					builder.WriteString(fmt.Sprintf("[green][%s] Me:[-] %s\n", timeStr, msg.Text))
				} else {
					sender := msg.Sender
					if len(sender) > MaxSenderNameLength {
						sender = sender[:MaxSenderNameLength-3] + "..."
					}
					builder.WriteString(fmt.Sprintf("[cyan][%s] %s:[-] %s\n", timeStr, sender, msg.Text))
				}
			}
			t.msgView.SetText(builder.String())
		}
	} else {
		t.msgView.SetText("[yellow]No conversations found. Make sure Messages is configured and Full Disk Access is granted.[-]")
	}
}

func (t *MessagesTUI) loadConversations() {
	convs := t.watcher.GetConversations(DefaultConversationLimit)

	t.mu.Lock()
	t.conversations = convs
	t.mu.Unlock()

	t.app.QueueUpdateDraw(func() {
		t.convList.Clear()
		for _, conv := range convs {
			name := conv.DisplayName
			if len(name) > MaxDisplayNameLength {
				name = name[:MaxDisplayNameLength-3] + "..."
			}

			secondary := t.formatTime(conv.LastMessageDate)
			if conv.UnreadCount > 0 {
				name = fmt.Sprintf("(%d) %s", conv.UnreadCount, name)
			}

			t.convList.AddItem(name, secondary, 0, nil)
		}

		if len(convs) > 0 && t.selectedChatID == 0 {
			t.selectedChatID = convs[0].ChatID
			// Run in goroutine to avoid deadlock from nested QueueUpdateDraw
			go t.loadMessages(convs[0].ChatID)
		}
	})
}

func (t *MessagesTUI) loadMessages(chatID int64) {
	// Show loading indicator
	t.app.QueueUpdateDraw(func() {
		t.msgView.SetText("[yellow]Loading messages...[-]")
	})

	msgs := t.watcher.GetMessages(chatID, DefaultMessageLimit)

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

		if msgs == nil {
			t.msgView.SetText("[red]Unable to load messages[-]")
			return
		}

		var builder strings.Builder
		for _, msg := range msgs {
			timeStr := t.formatTime(msg.Date)

			if msg.IsFromMe {
				builder.WriteString(fmt.Sprintf("[green][%s] Me:[-] %s\n", timeStr, msg.Text))
			} else {
				sender := msg.Sender
				if len(sender) > MaxSenderNameLength {
					sender = sender[:MaxSenderNameLength-3] + "..."
				}
				builder.WriteString(fmt.Sprintf("[cyan][%s] %s:[-] %s\n", timeStr, sender, msg.Text))
			}
		}
		t.msgView.SetText(builder.String())
		t.msgView.ScrollToEnd()
	})
}

func (t *MessagesTUI) sendMessage(text string) {
	// Prevent multiple concurrent sends
	if !t.sendingMessage.CompareAndSwap(false, true) {
		t.app.QueueUpdateDraw(func() {
			t.setStatus("⏳ Already sending a message...")
		})
		return
	}

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
		t.sendingMessage.Store(false)
		t.app.QueueUpdateDraw(func() {
			t.setStatus("Error: No conversation selected")
		})
		return
	}

	// Run async to avoid blocking UI (AppleScript can take up to 30s)
	go func() {
		defer t.sendingMessage.Store(false)

		t.app.QueueUpdateDraw(func() {
			t.setStatus("📤 Sending...")
		})

		err := sender.SendMessage(chatIdent, text)
		if err != nil {
			t.app.QueueUpdateDraw(func() {
				t.setStatus(fmt.Sprintf("❌ Error: %v", err))
				// Restore the message text so user can retry
				t.inputField.SetText(text)
			})
		} else {
			t.app.QueueUpdateDraw(func() {
				t.setStatus("✓ Message sent!")
			})
			// Refresh messages after a short delay
			time.Sleep(MessageRefreshDelay)
			t.loadMessages(chatID)
		}
	}()
}

func (t *MessagesTUI) refresh() {
	// Prevent concurrent refreshes
	if !t.refreshing.CompareAndSwap(false, true) {
		return
	}

	// Set status directly - we're on the main event loop thread
	t.setStatus("🔄 Refreshing...")

	// Run refresh in goroutine to avoid blocking UI
	go func() {
		defer t.refreshing.Store(false)

		// Use channels to fetch data with timeout
		type convResult struct {
			convs []watcher.Conversation
		}
		type msgResult struct {
			msgs []watcher.Message
		}

		convCh := make(chan convResult, 1)
		go func() {
			convCh <- convResult{convs: t.watcher.GetConversations(DefaultConversationLimit)}
		}()

		// Wait for conversations with timeout
		var convs []watcher.Conversation
		select {
		case res := <-convCh:
			convs = res.convs
		case <-time.After(5 * time.Second):
			t.app.QueueUpdateDraw(func() {
				t.setStatus("⚠️ Refresh timeout - database may be busy")
			})
			return
		}

		t.mu.Lock()
		t.conversations = convs
		chatID := t.selectedChatID
		t.mu.Unlock()

		// Fetch messages before updating UI (if we have a selected chat)
		var msgs []watcher.Message
		var chatName string
		if chatID > 0 {
			msgCh := make(chan msgResult, 1)
			go func() {
				msgCh <- msgResult{msgs: t.watcher.GetMessages(chatID, DefaultMessageLimit)}
			}()

			// Wait for messages with timeout
			select {
			case res := <-msgCh:
				msgs = res.msgs
			case <-time.After(5 * time.Second):
				t.app.QueueUpdateDraw(func() {
					t.setStatus("⚠️ Message load timeout - database may be busy")
				})
				return
			}

			t.mu.Lock()
			t.messages = msgs
			t.mu.Unlock()

			// Find conversation name
			t.mu.RLock()
			for _, conv := range t.conversations {
				if conv.ChatID == chatID {
					chatName = conv.DisplayName
					break
				}
			}
			t.mu.RUnlock()
		}

		// Single QueueUpdateDraw call to update all UI elements atomically
		t.app.QueueUpdateDraw(func() {
			// Update conversation list
			t.convList.Clear()
			for _, conv := range convs {
				name := conv.DisplayName
				if len(name) > MaxDisplayNameLength {
					name = name[:MaxDisplayNameLength-3] + "..."
				}

				secondary := t.formatTime(conv.LastMessageDate)
				if conv.UnreadCount > 0 {
					name = fmt.Sprintf("(%d) %s", conv.UnreadCount, name)
				}

				t.convList.AddItem(name, secondary, 0, nil)
			}

			// Update messages if we have a selected chat
			if chatID > 0 && msgs != nil {
				t.msgView.Clear()
				t.msgView.SetTitle(fmt.Sprintf(" %s ", chatName))

				var builder strings.Builder
				for _, msg := range msgs {
					timeStr := t.formatTime(msg.Date)
					if msg.IsFromMe {
						builder.WriteString(fmt.Sprintf("[green][%s] Me:[-] %s\n", timeStr, msg.Text))
					} else {
						sender := msg.Sender
						if len(sender) > MaxSenderNameLength {
							sender = sender[:MaxSenderNameLength-3] + "..."
						}
						builder.WriteString(fmt.Sprintf("[cyan][%s] %s:[-] %s\n", timeStr, sender, msg.Text))
					}
				}
				t.msgView.SetText(builder.String())
				t.msgView.ScrollToEnd()
			}

			t.setStatus("✓ Refreshed!")
		})
	}()
}

func (t *MessagesTUI) onNewMessages(msgs []watcher.Message) {
	if t.logger != nil {
		t.logf("onNewMessages: received %d messages", len(msgs))
	}
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
	if t.logger != nil {
		t.logf("onConversationsUpdated: got %d convs", len(convs))
	}
	t.mu.Lock()
	t.conversations = convs
	t.mu.Unlock()

	t.app.QueueUpdateDraw(func() {
		// Preserve selection
		selectedIdx := t.convList.GetCurrentItem()

		t.convList.Clear()
		for _, conv := range convs {
			name := conv.DisplayName
			if len(name) > MaxDisplayNameLength {
				name = name[:MaxDisplayNameLength-3] + "..."
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

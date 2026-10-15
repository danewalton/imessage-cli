// Package tui provides the text user interface for iMessage CLI.
package tui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/danewalton/imessage-cli/internal/database"
	"github.com/danewalton/imessage-cli/internal/sender"
	"github.com/danewalton/imessage-cli/internal/watcher"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type messageWatcher interface {
	GetConversations(int) ([]watcher.Conversation, error)
	GetMessages(int64, int) ([]watcher.Message, error)
	OnNewMessages(watcher.MessageCallback)
	OnConversationsUpdated(watcher.ConversationCallback)
	OnError(watcher.ErrorCallback)
	Start()
	Stop()
}

// UI constants
const (
	DefaultConversationLimit = 50
	DefaultMessageLimit      = 100
	MaxDisplayNameLength     = 30
	MaxSenderNameLength      = 15
	MessageRefreshDelay      = 500 * time.Millisecond
	LockFileName             = ".imessage-tui.lock"
	PreviewMaxWidth          = 80
	PreviewMaxHeight         = 30
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

	watcher         messageWatcher
	conversations   []watcher.Conversation
	messages        []watcher.Message
	selectedChatID  int64
	selectedChatIdx int
	previewModal    *tview.TextView

	rebuildingList bool
	loadGeneration uint64
	draftVersion   uint64
	drafts         map[int64]string
	failedDrafts   map[int64][]string
	send           func(watcher.Conversation, string) error
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
		watcher:      watcher.NewMessageWatcher(500 * time.Millisecond),
		drafts:       make(map[int64]string),
		failedDrafts: make(map[int64][]string),
		send: func(conv watcher.Conversation, text string) error {
			return sender.SendToConversation(database.Conversation{
				ChatID: conv.ChatID, ChatGUID: conv.ChatGUID,
			}, text)
		},
	}
}

func openDebugLog(logPath string) (*os.File, error) {
	if logPath == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(cacheDir, "imessage-cli")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("debug log directory is not a directory: %s", dir)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			return nil, fmt.Errorf("debug log directory is not owned by the current user")
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return nil, err
		}
		logPath = filepath.Join(dir, "tui.log")
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
			err = fmt.Errorf("debug log must be a regular file owned only by the current user")
		}
	}
	if err == nil {
		err = f.Chmod(0600)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
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
		f, err := openDebugLog(logPath)
		if err != nil {
			return fmt.Errorf("unable to open log file: %w", err)
		}
		t.logFile = f
		t.logger = log.New(f, "tui: ", log.LstdFlags|log.Lmicroseconds)
		t.logf("debug logging enabled, file=%s", f.Name())
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
	t.watcher.OnError(func(err error) {
		t.app.QueueUpdateDraw(func() {
			t.setStatus(fmt.Sprintf("Database error: %v (check Full Disk Access)", err))
		})
	})

	// Load initial data synchronously (before app.Run)
	if err := t.loadInitialData(); err != nil {
		return fmt.Errorf("load conversations: %w (check Full Disk Access)", err)
	}

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
		if !t.rebuildingList {
			t.selectConversation(index)
		}
	})

	t.convList.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		t.selectConversation(index)
		t.app.SetFocus(t.msgView)
		t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  p:Preview  r:Refresh  q:Quit")
	})

	// Input handling
	t.inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			text := t.inputField.GetText()
			if text != "" {
				t.sendMessage(text)
			}
			// Keep focus on the input field so the user can keep typing
			t.app.SetFocus(t.inputField)
		} else if key == tcell.KeyEscape {
			t.app.SetFocus(t.msgView)
			t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  p:Preview  r:Refresh  q:Quit")
		}
	})
	t.inputField.SetChangedFunc(func(text string) {
		t.draftVersion++
		if t.selectedChatID != 0 {
			t.drafts[t.selectedChatID] = text
		}
	})

	// Global key handling
	t.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		focused := t.app.GetFocus()
		if t.logger != nil {
			t.logf("input event: key=%v focused=%T", event.Key(), focused)
		}

		if event.Key() == tcell.KeyCtrlR {
			t.recoverFailedDraft()
			return nil
		}
		// Handle input field separately
		if focused == t.inputField {
			return event
		}

		switch event.Key() {
		case tcell.KeyTab:
			if focused == t.convList {
				t.app.SetFocus(t.msgView)
				t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  p:Preview  r:Refresh  q:Quit")
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
				t.setStatus("[INPUT] Enter:Send  Esc:Back  Ctrl+R:Recover failed message")
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
					t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  p:Preview  r:Refresh  q:Quit")
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
			case 'p':
				if focused == t.msgView {
					att := t.findNearestImageAttachment()
					if att != nil {
						t.showImagePreview(*att)
					} else {
						t.setStatus("No image attachments in this conversation")
					}
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
				t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  p:Preview  r:Refresh  q:Quit")
				return nil
			}
		}

		return event
	})
}

func (t *MessagesTUI) setStatus(msg string) {
	t.statusBar.SetText(" " + tview.Escape(msg) + " ")
}

func (t *MessagesTUI) logf(format string, v ...interface{}) {
	if t.logger != nil {
		t.logger.Printf(format, v...)
	}
}

// loadInitialData loads data synchronously before the app starts
func (t *MessagesTUI) loadInitialData() error {
	convs, err := t.watcher.GetConversations(DefaultConversationLimit)
	if err != nil {
		return err
	}
	if len(convs) > 0 {
		t.selectedChatID = convs[0].ChatID
		t.messages, err = t.watcher.GetMessages(t.selectedChatID, DefaultMessageLimit)
		if err != nil {
			return err
		}
	}
	t.applyConversations(convs)
	t.renderMessages()
	return nil
}

// List and selection changes run on the UI thread, or before Run starts.
func (t *MessagesTUI) applyConversations(convs []watcher.Conversation) bool {
	selected := 0
	for i, conv := range convs {
		if conv.ChatID == t.selectedChatID {
			selected = i
			break
		}
	}
	t.conversations = convs
	t.rebuildingList = true
	t.convList.Clear()
	for _, conv := range convs {
		name := truncateName(conv.DisplayName, MaxDisplayNameLength)
		if conv.UnreadCount > 0 {
			name = fmt.Sprintf("(%d) %s", conv.UnreadCount, name)
		}
		t.convList.AddItem(tview.Escape(name), t.formatTime(conv.LastMessageDate), 0, nil)
	}
	if len(convs) > 0 {
		t.convList.SetCurrentItem(selected)
	} else {
		selected = -1
	}
	t.rebuildingList = false
	return t.selectConversation(selected)
}

func (t *MessagesTUI) selectConversation(index int) bool {
	var conv watcher.Conversation
	if index >= 0 && index < len(t.conversations) {
		conv = t.conversations[index]
	}
	t.selectedChatIdx = index
	t.inputField.SetLabel("Send: ")
	if conv.ChatID != 0 {
		t.inputField.SetLabel("To " + tview.Escape(truncateName(conv.DisplayName, MaxDisplayNameLength)) + ": ")
	}
	if conv.ChatID == t.selectedChatID {
		return false
	}
	if t.selectedChatID != 0 {
		t.drafts[t.selectedChatID] = t.inputField.GetText()
	}
	t.selectedChatID = conv.ChatID
	t.loadGeneration++
	t.messages = nil
	t.inputField.SetText(t.drafts[conv.ChatID])
	t.renderMessages()
	if conv.ChatID != 0 {
		t.loadMessages(conv.ChatID)
	}
	return true
}

func truncateName(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-3]) + "..."
	}
	return text
}

func (t *MessagesTUI) renderMessages() {
	t.msgView.SetTitle(" Messages ")
	for _, conv := range t.conversations {
		if conv.ChatID == t.selectedChatID {
			t.msgView.SetTitle(" " + tview.Escape(conv.DisplayName) + " ")
			break
		}
	}
	if t.selectedChatID == 0 {
		t.msgView.SetText("[yellow]No conversations found.[-]")
		return
	}
	if len(t.messages) == 0 {
		t.msgView.SetText("[yellow]No messages in this conversation.[-]")
		return
	}
	var builder strings.Builder
	for _, msg := range t.messages {
		t.formatMessageLine(&builder, msg)
	}
	t.msgView.SetText(builder.String())
	t.msgView.ScrollToEnd()
}

func (t *MessagesTUI) loadMessages(chatID int64) {
	if chatID == 0 || chatID != t.selectedChatID {
		return
	}
	t.loadGeneration++
	generation := t.loadGeneration
	go func() {
		type result struct {
			messages []watcher.Message
			err      error
		}
		results := make(chan result, 1)
		go func() {
			msgs, err := t.watcher.GetMessages(chatID, DefaultMessageLimit)
			results <- result{msgs, err}
		}()
		var res result
		select {
		case res = <-results:
		case <-time.After(5 * time.Second):
			res.err = fmt.Errorf("message load timed out; database may be busy")
		}
		t.app.QueueUpdateDraw(func() {
			t.applyMessages(chatID, generation, res.messages, res.err)
		})
	}()
}

func (t *MessagesTUI) applyMessages(chatID int64, generation uint64, msgs []watcher.Message, err error) {
	// A slow read must not replace a newer selection or refresh.
	if chatID != t.selectedChatID || generation != t.loadGeneration {
		return
	}
	if err != nil {
		t.setStatus(fmt.Sprintf("Unable to load messages: %v", err))
		if len(t.messages) == 0 {
			t.msgView.SetText("[red]" + tview.Escape(err.Error()) + "[-]")
		}
		return
	}
	t.messages = msgs
	t.renderMessages()
}

func (t *MessagesTUI) sendMessage(text string) bool {
	if !t.sendingMessage.CompareAndSwap(false, true) {
		t.setStatus("Already sending a message; your draft is unchanged")
		return false
	}
	chatID := t.selectedChatID
	var target watcher.Conversation
	for _, conv := range t.conversations {
		if conv.ChatID == chatID {
			target = conv
			break
		}
	}
	if target.ChatID == 0 {
		t.sendingMessage.Store(false)
		t.setStatus("Error: No conversation selected; your draft is unchanged")
		return false
	}
	t.inputField.SetText("")
	version := t.draftVersion
	t.setStatus("Sending to " + target.DisplayName + "...")
	go func() {
		err := t.send(target, text)
		t.app.QueueUpdateDraw(func() {
			t.finishSend(chatID, target.DisplayName, text, version, err)
		})
		if err == nil {
			time.Sleep(MessageRefreshDelay)
			t.app.QueueUpdateDraw(func() {
				t.loadMessages(chatID)
			})
		}
	}()
	return true
}

func (t *MessagesTUI) finishSend(chatID int64, name, text string, version uint64, err error) {
	t.sendingMessage.Store(false)
	if err == nil {
		t.setStatus("Message sent to " + name)
		return
	}
	// An empty composer may still have been edited and cleared since submission.
	if t.selectedChatID == chatID && t.draftVersion == version && t.inputField.GetText() == "" {
		t.inputField.SetText(text)
		t.setStatus(fmt.Sprintf("Send to %s failed: %v; draft restored", name, err))
		return
	}
	t.failedDrafts[chatID] = append(t.failedDrafts[chatID], text)
	t.setStatus(fmt.Sprintf("Send to %s failed: %v; select that chat and use Ctrl+R to recover", name, err))
}

func (t *MessagesTUI) recoverFailedDraft() {
	if t.inputField.GetText() != "" {
		t.setStatus("Send or clear the current draft before recovering a failed message")
		return
	}
	failed := t.failedDrafts[t.selectedChatID]
	if len(failed) == 0 {
		t.setStatus("No failed messages to recover in this conversation")
		return
	}
	t.inputField.SetText(failed[0])
	t.failedDrafts[t.selectedChatID] = failed[1:]
	t.app.SetFocus(t.inputField)
	t.setStatus("Failed message recovered; review it before pressing Enter")
}

func (t *MessagesTUI) refresh() {
	if !t.refreshing.CompareAndSwap(false, true) {
		return
	}
	t.setStatus("Refreshing...")
	go func() {
		type result struct {
			convs []watcher.Conversation
			err   error
		}
		results := make(chan result, 1)
		go func() {
			convs, err := t.watcher.GetConversations(DefaultConversationLimit)
			results <- result{convs, err}
		}()
		var res result
		select {
		case res = <-results:
		case <-time.After(5 * time.Second):
			res.err = fmt.Errorf("refresh timed out; database may be busy")
		}
		t.app.QueueUpdateDraw(func() {
			t.refreshing.Store(false)
			if res.err != nil {
				t.setStatus("Refresh failed: " + res.err.Error())
				return
			}
			if !t.applyConversations(res.convs) {
				t.loadMessages(t.selectedChatID)
			}
			t.setStatus("Conversations refreshed")
		})
	}()
}

func (t *MessagesTUI) onNewMessages(msgs []watcher.Message) {
	t.app.QueueUpdateDraw(func() {
		for _, msg := range msgs {
			if msg.ChatID == t.selectedChatID {
				t.loadMessages(t.selectedChatID)
				break
			}
		}
		if len(msgs) > 0 && !msgs[len(msgs)-1].IsFromMe {
			t.setStatus("New message from " + msgs[len(msgs)-1].Sender)
		}
	})
}

func (t *MessagesTUI) onConversationsUpdated(convs []watcher.Conversation) {
	t.app.QueueUpdateDraw(func() {
		t.applyConversations(convs)
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

// formatMessageLine renders a single message (with attachment info) into the builder.
func (t *MessagesTUI) formatMessageLine(builder *strings.Builder, msg watcher.Message) {
	timeStr := t.formatTime(msg.Date)
	if msg.IsFromMe {
		builder.WriteString(fmt.Sprintf("[green][%s] Me:[-] %s\n", timeStr, tview.Escape(msg.Text)))
	} else {
		name := truncateName(msg.Sender, MaxSenderNameLength)
		builder.WriteString(fmt.Sprintf("[cyan][%s] %s:[-] %s\n", timeStr, tview.Escape(name), tview.Escape(msg.Text)))
	}

	// Show attachment indicators
	for _, att := range msg.Attachments {
		if att.IsImage {
			builder.WriteString(fmt.Sprintf("              [yellow]📎 %s (image · p to preview)[-]\n", tview.Escape(att.Filename)))
		} else {
			builder.WriteString(fmt.Sprintf("              [gray]📎 %s[-]\n", tview.Escape(att.Filename)))
		}
	}
}

// showImagePreview shows a modal with a half-block rendered image.
func (t *MessagesTUI) showImagePreview(att watcher.Attachment) {
	go func() {
		t.app.QueueUpdateDraw(func() {
			t.setStatus("🖼️  Rendering preview...")
		})

		rendered, err := RenderImageToText(att.FilePath, PreviewMaxWidth, PreviewMaxHeight)

		t.app.QueueUpdateDraw(func() {
			if err != nil {
				t.setStatus(fmt.Sprintf("❌ Preview failed: %v", err))
				return
			}

			if t.previewModal == nil {
				t.previewModal = tview.NewTextView().
					SetDynamicColors(true).
					SetScrollable(true).
					SetWrap(false)
				t.previewModal.SetBorder(true).
					SetBorderColor(tcell.ColorYellow)
				t.previewModal.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
					switch event.Key() {
					case tcell.KeyEscape, tcell.KeyEnter:
						t.pages.RemovePage("preview")
						t.app.SetFocus(t.msgView)
						t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  p:Preview  r:Refresh  q:Quit")
						return nil
					case tcell.KeyRune:
						if event.Rune() == 'q' {
							t.pages.RemovePage("preview")
							t.app.SetFocus(t.msgView)
							t.setStatus("[MSG] ↑↓:Scroll  h/←:Back  i:Input  p:Preview  r:Refresh  q:Quit")
							return nil
						}
					}
					return event
				})
			}

			title := fmt.Sprintf(" 🖼️  %s (Esc to close) ", tview.Escape(att.Filename))
			t.previewModal.SetTitle(title)
			t.previewModal.SetText(rendered)
			t.previewModal.ScrollToBeginning()

			// Center the modal as an overlay
			modal := tview.NewFlex().
				AddItem(nil, 0, 1, false).
				AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
					AddItem(nil, 0, 1, false).
					AddItem(t.previewModal, PreviewMaxHeight+2, 0, true).
					AddItem(nil, 0, 1, false), PreviewMaxWidth+2, 0, true).
				AddItem(nil, 0, 1, false)

			t.pages.AddPage("preview", modal, true, true)
			t.app.SetFocus(t.previewModal)
			t.setStatus("[PREVIEW] Esc/Enter/q:Close  ↑↓:Scroll")
		})
	}()
}

// findNearestImageAttachment scans messages for the nearest image attachment,
// searching backwards from the most recent message.
func (t *MessagesTUI) findNearestImageAttachment() *watcher.Attachment {
	// Search from newest to oldest
	for i := len(t.messages) - 1; i >= 0; i-- {
		for _, att := range t.messages[i].Attachments {
			if att.IsImage {
				a := att // copy
				return &a
			}
		}
	}
	return nil
}

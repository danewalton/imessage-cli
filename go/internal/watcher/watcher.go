// Package watcher provides real-time message watching functionality.
package watcher

import (
	"database/sql"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/danewalton/imessage-cli/internal/database"
)

// Watcher constants
const (
	DefaultPollInterval      = 500 * time.Millisecond
	DefaultConversationLimit = 50
)

// Message represents an iMessage for the watcher.
type Message struct {
	MessageID      int64
	Text           string
	Date           *time.Time
	IsFromMe       bool
	IsRead         bool
	Sender         string
	ChatID         int64
	ChatIdentifier string
	ChatName       string
}

// Conversation represents a conversation for the watcher.
type Conversation struct {
	ChatID          int64
	ChatIdentifier  string
	DisplayName     string
	Service         string
	LastMessageDate *time.Time
	LastMessageText string
	UnreadCount     int
	Participants    []string
}

// MessageCallback is called when new messages arrive.
type MessageCallback func([]Message)

// ConversationCallback is called when conversations are updated.
type ConversationCallback func([]Conversation)

// ErrorCallback is called when an error occurs.
type ErrorCallback func(error)

// MessageWatcher watches the iMessage database for new messages.
type MessageWatcher struct {
	pollInterval          time.Duration
	running               bool
	lastMessageID         atomic.Int64
	lastMtime             atomic.Int64
	messageCallbacks      []MessageCallback
	conversationCallbacks []ConversationCallback
	errorCallbacks        []ErrorCallback
	mu                    sync.RWMutex
	stopCh                chan struct{}
	wg                    sync.WaitGroup
	// logger for debugging callback issues
	logger *log.Logger
}

// NewMessageWatcher creates a new MessageWatcher.
func NewMessageWatcher(pollInterval time.Duration) *MessageWatcher {
	return &MessageWatcher{
		pollInterval: pollInterval,
		stopCh:       make(chan struct{}),
	}
}

// OnNewMessages registers a callback for new messages.
func (w *MessageWatcher) OnNewMessages(callback MessageCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messageCallbacks = append(w.messageCallbacks, callback)
}

// OnConversationsUpdated registers a callback for conversation updates.
func (w *MessageWatcher) OnConversationsUpdated(callback ConversationCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conversationCallbacks = append(w.conversationCallbacks, callback)
}

// OnError registers a callback for errors.
func (w *MessageWatcher) OnError(callback ErrorCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.errorCallbacks = append(w.errorCallbacks, callback)
}

func (w *MessageWatcher) getLastMessageID() int64 {
	db, err := database.GetConnection()
	if err != nil {
		return 0
	}
	defer db.Close()

	var maxID sql.NullInt64
	err = db.QueryRow("SELECT MAX(ROWID) FROM message").Scan(&maxID)
	if err != nil || !maxID.Valid {
		return 0
	}
	return maxID.Int64
}

func (w *MessageWatcher) getDBMtime() int64 {
	dbPath := database.GetDBPath()
	info, err := os.Stat(dbPath)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

// GetConversations returns a list of conversations.
func (w *MessageWatcher) GetConversations(limit int) []Conversation {
	convs, err := database.GetConversations(limit)
	if err != nil {
		return nil
	}

	var result []Conversation
	for _, c := range convs {
		result = append(result, Conversation{
			ChatID:          c.ChatID,
			ChatIdentifier:  c.ChatIdentifier,
			DisplayName:     c.DisplayName,
			Service:         c.Service,
			LastMessageDate: c.LastMessageDate,
			LastMessageText: c.LastMessageText,
			UnreadCount:     c.UnreadCount,
			Participants:    c.Participants,
		})
	}
	return result
}

// GetMessages returns messages for a specific chat.
func (w *MessageWatcher) GetMessages(chatID int64, limit int) []Message {
	msgs, err := database.GetMessages(chatID, "", limit)
	if err != nil {
		return nil
	}

	var result []Message
	for _, m := range msgs {
		result = append(result, Message{
			MessageID:      m.MessageID,
			Text:           m.Text,
			Date:           m.Date,
			IsFromMe:       m.IsFromMe,
			IsRead:         m.IsRead,
			Sender:         m.Sender,
			ChatID:         m.ChatID,
			ChatIdentifier: m.ChatIdent,
			ChatName:       m.ChatName,
		})
	}
	return result
}

// GetNewMessages returns messages newer than the given ID.
func (w *MessageWatcher) GetNewMessages(sinceID int64) []Message {
	db, err := database.GetConnection()
	if err != nil {
		w.notifyError(err)
		return nil
	}
	defer db.Close()

	query := `
		SELECT 
			m.ROWID as message_id,
			m.text,
			m.attributedBody,
			m.date,
			m.is_from_me,
			m.is_read,
			h.id as sender_id,
			c.ROWID as chat_id,
			c.chat_identifier,
			c.display_name
		FROM message m
		LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
		LEFT JOIN chat c ON cmj.chat_id = c.ROWID
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE m.ROWID > ?
		ORDER BY m.date ASC
	`

	rows, err := db.Query(query, sinceID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var text, senderID, chatIdent, chatName sql.NullString
		var attributedBody []byte
		var date sql.NullInt64
		var isFromMe, isRead int

		err := rows.Scan(&m.MessageID, &text, &attributedBody, &date, &isFromMe, &isRead, &senderID, &m.ChatID, &chatIdent, &chatName)
		if err != nil {
			continue
		}

		m.IsFromMe = isFromMe == 1
		m.IsRead = isRead == 1
		m.ChatIdentifier = chatIdent.String
		m.ChatName = chatName.String

		if date.Valid {
			m.Date = database.AppleTimeToTime(date.Int64)
		}

		m.Text = text.String
		if m.Text == "" && len(attributedBody) > 0 {
			m.Text = database.ExtractTextFromAttributedBody(attributedBody)
		}
		if m.Text == "" {
			m.Text = "[Attachment]"
		}

		m.Sender = database.ResolveSender(m.IsFromMe, senderID.String)

		if m.ChatName == "" {
			m.ChatName = database.GetContactName(m.ChatIdentifier)
		}

		messages = append(messages, m)
	}

	return messages
}

func (w *MessageWatcher) pollLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.poll()
		}
	}
}

func (w *MessageWatcher) poll() {
	// Check if database has been modified
	currentMtime := w.getDBMtime()
	lastMtime := w.lastMtime.Load()

	if currentMtime > lastMtime {
		w.lastMtime.Store(currentMtime)

		// Check for new messages
		currentMaxID := w.getLastMessageID()
		lastID := w.lastMessageID.Load()

		if currentMaxID > lastID {
			newMessages := w.GetNewMessages(lastID)
			w.lastMessageID.Store(currentMaxID)

			if len(newMessages) > 0 {
				w.mu.RLock()
				callbacks := make([]MessageCallback, len(w.messageCallbacks))
				copy(callbacks, w.messageCallbacks)
				w.mu.RUnlock()

				for _, cb := range callbacks {
					go func(callback MessageCallback, msgs []Message) {
						defer func() {
							if r := recover(); r != nil {
								if w.logger != nil {
									w.logger.Printf("panic in message callback: %v", r)
								}
							}
						}()
						callback(msgs)
					}(cb, newMessages)
				}
			}
		}

		// Update conversations
		conversations := w.GetConversations(DefaultConversationLimit)
		w.mu.RLock()
		callbacks := make([]ConversationCallback, len(w.conversationCallbacks))
		copy(callbacks, w.conversationCallbacks)
		w.mu.RUnlock()

		for _, cb := range callbacks {
			go func(callback ConversationCallback, convs []Conversation) {
				defer func() {
					if r := recover(); r != nil {
						if w.logger != nil {
							w.logger.Printf("panic in conversation callback: %v", r)
						}
					}
				}()
				callback(convs)
			}(cb, conversations)
		}
	}
}

func (w *MessageWatcher) notifyError(err error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, cb := range w.errorCallbacks {
		go cb(err)
	}
}

// Start begins watching for new messages.
func (w *MessageWatcher) Start() {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}

	// Mark running and create stop channel immediately to avoid blocking
	w.running = true
	w.stopCh = make(chan struct{})
	w.mu.Unlock()

	// Start poll loop in a goroutine; perform initial DB checks there to avoid blocking caller
	w.wg.Add(1)
	go func() {
		// Initialize last IDs / mtime inside goroutine using atomic operations
		w.lastMessageID.Store(w.getLastMessageID())
		w.lastMtime.Store(w.getDBMtime())

		w.pollLoop()
	}()
}

// Stop stops watching for messages.
func (w *MessageWatcher) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	close(w.stopCh)
	w.mu.Unlock()

	w.wg.Wait()
}

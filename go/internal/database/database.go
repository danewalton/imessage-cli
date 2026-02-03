// Package database provides functionality for reading iMessage data from chat.db.
package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	_ "github.com/mattn/go-sqlite3"
)

// Message represents an iMessage.
type Message struct {
	MessageID int64
	Text      string
	Date      *time.Time
	IsFromMe  bool
	IsRead    bool
	Service   string
	Sender    string
	ChatID    int64
	ChatIdent string
	ChatName  string
}

// Conversation represents a chat/conversation.
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

// GetDBPath returns the path to the iMessage database.
func GetDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Messages", "chat.db")
}

// GetConnection creates a read-only connection to the iMessage database.
func GetConnection() (*sql.DB, error) {
	dbPath := GetDBPath()
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("iMessage database not found at %s. Make sure you're running this on macOS with Messages configured", dbPath)
	}

	// Connect in read-only mode
	connStr := fmt.Sprintf("file:%s?mode=ro", dbPath)
	return sql.Open("sqlite3", connStr)
}

// AppleTimeToTime converts Apple's timestamp format to Go time.Time.
// Apple uses nanoseconds since 2001-01-01, while Unix uses seconds since 1970-01-01.
// The difference is 978307200 seconds.
func AppleTimeToTime(appleTime int64) *time.Time {
	if appleTime == 0 {
		return nil
	}

	const appleEpochOffset = 978307200

	var unixTimestamp float64
	if appleTime > 1e18 {
		// Nanoseconds
		unixTimestamp = float64(appleTime)/1e9 + appleEpochOffset
	} else if appleTime > 1e9 {
		// Already in reasonable range, might be nanoseconds
		unixTimestamp = float64(appleTime)/1e9 + appleEpochOffset
	} else {
		unixTimestamp = float64(appleTime) + appleEpochOffset
	}

	t := time.Unix(int64(unixTimestamp), 0)
	return &t
}

// ExtractTextFromAttributedBody extracts plain text from an attributedBody blob.
// The attributedBody column contains a serialized NSAttributedString.
func ExtractTextFromAttributedBody(data []byte) string {
	if data == nil || len(data) == 0 {
		return ""
	}

	// Decode as UTF-8, replacing invalid characters
	decoded := string(data)

	// Method 1: The attributed body contains serialized NSAttributedString data
	if strings.Contains(decoded, "NSNumber") {
		temp := strings.Split(decoded, "NSNumber")[0]
		if strings.Contains(temp, "NSString") {
			temp = strings.Split(temp, "NSString")[1]
			if strings.Contains(temp, "NSDictionary") {
				temp = strings.Split(temp, "NSDictionary")[0]
				// Remove leading/trailing serialization bytes
				if len(temp) > 18 {
					temp = temp[6 : len(temp)-12]
				}
				// Clean up the text
				cleaned := cleanPrintable(temp)
				if len(strings.TrimSpace(cleaned)) > 0 {
					return strings.TrimSpace(cleaned)
				}
			}
		}
	}

	// Method 2: Try to find text after streamtyped marker
	if strings.Contains(string(data), "streamtyped") {
		parts := strings.Split(decoded, "NSString")
		if len(parts) > 1 {
			textPart := parts[1]
			cleaned := cleanPrintable(textPart)
			// Find where the actual text ends (before next marker)
			for _, marker := range []string{"NSDictionary", "NSNumber", "NSArray"} {
				if strings.Contains(cleaned, marker) {
					cleaned = strings.Split(cleaned, marker)[0]
				}
			}
			cleaned = strings.TrimSpace(cleaned)
			if len(cleaned) > 1 {
				return cleaned
			}
		}
	}

	// Method 3: Look for any readable text using regex
	re := regexp.MustCompile(`[\x20-\x7E\u00A0-\uFFFF]{3,}`)
	matches := re.FindAllString(decoded, -1)
	if len(matches) > 0 {
		// Filter out known serialization artifacts
		artifacts := []string{"bplist", "NSString", "NSNumber", "NSDictionary",
			"NSArray", "NSData", "$class", "archiver", "streamtyped"}

		var filtered []string
		for _, m := range matches {
			hasArtifact := false
			for _, artifact := range artifacts {
				if strings.Contains(m, artifact) {
					hasArtifact = true
					break
				}
			}
			if !hasArtifact && len(strings.TrimSpace(m)) > 2 {
				filtered = append(filtered, strings.TrimSpace(m))
			}
		}

		if len(filtered) > 0 {
			// Return the longest match
			longest := filtered[0]
			for _, f := range filtered {
				if len(f) > len(longest) {
					longest = f
				}
			}
			return longest
		}
	}

	return ""
}

func cleanPrintable(s string) string {
	var result strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) || r == '\n' || r == '\t' {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// GetConversations retrieves a list of recent conversations.
func GetConversations(limit int) ([]Conversation, error) {
	db, err := GetConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	query := `
		SELECT 
			c.ROWID as chat_id,
			c.chat_identifier,
			c.display_name,
			c.service_name,
			MAX(m.date) as last_message_date,
			GROUP_CONCAT(DISTINCT h.id) as participants
		FROM chat c
		LEFT JOIN chat_message_join cmj ON c.ROWID = cmj.chat_id
		LEFT JOIN message m ON cmj.message_id = m.ROWID
		LEFT JOIN chat_handle_join chj ON c.ROWID = chj.chat_id
		LEFT JOIN handle h ON chj.handle_id = h.ROWID
		GROUP BY c.ROWID
		ORDER BY last_message_date DESC
		LIMIT ?
	`

	rows, err := db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conversations []Conversation
	for rows.Next() {
		var c Conversation
		var chatIdentifier, displayName, service sql.NullString
		var lastMessageDate sql.NullInt64
		var participants sql.NullString

		err := rows.Scan(&c.ChatID, &chatIdentifier, &displayName, &service, &lastMessageDate, &participants)
		if err != nil {
			continue
		}

		c.ChatIdentifier = chatIdentifier.String
		c.DisplayName = displayName.String
		c.Service = service.String
		if c.Service == "" {
			c.Service = "iMessage"
		}

		if lastMessageDate.Valid {
			c.LastMessageDate = AppleTimeToTime(lastMessageDate.Int64)
		}

		if participants.Valid && participants.String != "" {
			c.Participants = strings.Split(participants.String, ",")
		}

		// Resolve display name from contacts if not set
		if c.DisplayName == "" {
			c.DisplayName = GetContactName(c.ChatIdentifier)
		}

		conversations = append(conversations, c)
	}

	return conversations, nil
}

// GetMessages retrieves messages from a specific conversation.
func GetMessages(chatID int64, chatIdentifier string, limit int) ([]Message, error) {
	db, err := GetConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var whereClause string
	var whereParam interface{}
	if chatID > 0 {
		whereClause = "c.ROWID = ?"
		whereParam = chatID
	} else if chatIdentifier != "" {
		whereClause = "c.chat_identifier = ?"
		whereParam = chatIdentifier
	} else {
		return nil, fmt.Errorf("must provide either chat_id or chat_identifier")
	}

	query := fmt.Sprintf(`
		SELECT 
			m.ROWID as message_id,
			m.text,
			m.attributedBody,
			m.date,
			m.is_from_me,
			m.is_read,
			m.service,
			h.id as sender_id,
			c.ROWID as chat_id,
			c.chat_identifier,
			c.display_name
		FROM message m
		LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
		LEFT JOIN chat c ON cmj.chat_id = c.ROWID
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE %s
		ORDER BY m.date DESC
		LIMIT ?
	`, whereClause)

	rows, err := db.Query(query, whereParam, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		var text, senderID, chatIdent, chatName sql.NullString
		var attributedBody []byte
		var date sql.NullInt64
		var isFromMe, isRead int
		var service sql.NullString

		err := rows.Scan(&m.MessageID, &text, &attributedBody, &date, &isFromMe, &isRead, &service, &senderID, &m.ChatID, &chatIdent, &chatName)
		if err != nil {
			continue
		}

		m.IsFromMe = isFromMe == 1
		m.IsRead = isRead == 1
		m.Service = service.String
		m.ChatIdent = chatIdent.String
		m.ChatName = chatName.String

		if date.Valid {
			m.Date = AppleTimeToTime(date.Int64)
		}

		// Try to get text from the text column first, then fall back to attributedBody
		m.Text = text.String
		if m.Text == "" && len(attributedBody) > 0 {
			m.Text = ExtractTextFromAttributedBody(attributedBody)
		}
		if m.Text == "" {
			m.Text = "[Attachment]"
		}

		// Resolve sender
		m.Sender = ResolveSender(m.IsFromMe, senderID.String)

		// Resolve chat name
		if m.ChatName == "" {
			m.ChatName = GetContactName(m.ChatIdent)
		}

		messages = append(messages, m)
	}

	// Reverse to show oldest first
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// SearchMessages searches for messages containing the given text.
func SearchMessages(query string, limit int) ([]Message, error) {
	db, err := GetConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	sqlQuery := `
		SELECT 
			m.ROWID as message_id,
			m.text,
			m.attributedBody,
			m.date,
			m.is_from_me,
			c.chat_identifier,
			c.display_name,
			h.id as sender_id
		FROM message m
		LEFT JOIN chat_message_join cmj ON m.ROWID = cmj.message_id
		LEFT JOIN chat c ON cmj.chat_id = c.ROWID
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE m.text LIKE ? OR CAST(m.attributedBody AS TEXT) LIKE ?
		ORDER BY m.date DESC
		LIMIT ?
	`

	searchPattern := "%" + query + "%"
	rows, err := db.Query(sqlQuery, searchPattern, searchPattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Message
	for rows.Next() {
		var m Message
		var text, chatIdent, chatName, senderID sql.NullString
		var attributedBody []byte
		var date sql.NullInt64
		var isFromMe int

		err := rows.Scan(&m.MessageID, &text, &attributedBody, &date, &isFromMe, &chatIdent, &chatName, &senderID)
		if err != nil {
			continue
		}

		m.IsFromMe = isFromMe == 1
		m.ChatIdent = chatIdent.String
		m.ChatName = chatName.String

		if date.Valid {
			m.Date = AppleTimeToTime(date.Int64)
		}

		m.Text = text.String
		if m.Text == "" && len(attributedBody) > 0 {
			m.Text = ExtractTextFromAttributedBody(attributedBody)
		}
		if m.Text == "" {
			m.Text = "[Attachment]"
		}

		m.Sender = ResolveSender(m.IsFromMe, senderID.String)

		if m.ChatName == "" {
			m.ChatName = GetContactName(m.ChatIdent)
		}

		results = append(results, m)
	}

	return results, nil
}

// GetUnreadCount returns the count of unread messages.
func GetUnreadCount() (int, error) {
	db, err := GetConnection()
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var count int
	err = db.QueryRow(`
		SELECT COUNT(*) as count 
		FROM message 
		WHERE is_read = 0 AND is_from_me = 0
	`).Scan(&count)

	return count, err
}

// GetContactByIdentifier looks up a contact by phone number or email.
func GetContactByIdentifier(identifier string) (*Conversation, error) {
	db, err := GetConnection()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Normalize identifier
	normalized := normalizeIdentifier(identifier)

	var c Conversation
	var chatIdent, displayName, service sql.NullString

	err = db.QueryRow(`
		SELECT DISTINCT 
			h.id as identifier,
			h.service,
			c.chat_identifier,
			c.display_name
		FROM handle h
		LEFT JOIN chat_handle_join chj ON h.ROWID = chj.handle_id
		LEFT JOIN chat c ON chj.chat_id = c.ROWID
		WHERE h.id LIKE ? OR h.id LIKE ?
		LIMIT 1
	`, "%"+identifier+"%", "%"+normalized+"%").Scan(&chatIdent, &service, &c.ChatIdentifier, &displayName)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	c.DisplayName = displayName.String
	c.Service = service.String

	return &c, nil
}

func normalizeIdentifier(identifier string) string {
	var result strings.Builder
	for _, c := range identifier {
		if unicode.IsDigit(c) || c == '+' || c == '@' || c == '.' {
			result.WriteRune(c)
		}
	}
	return result.String()
}

// ResolveSender resolves a sender identifier to a display name.
func ResolveSender(isFromMe bool, senderID string) string {
	if isFromMe {
		return "Me"
	}
	if senderID != "" {
		return GetContactName(senderID)
	}
	return "Unknown"
}

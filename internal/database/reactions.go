package database

import (
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type ReactionType int

const (
	ReactionLove ReactionType = 2000 + iota
	ReactionLike
	ReactionDislike
	ReactionLaugh
	ReactionEmphasis
	ReactionQuestion
	ReactionEmoji
	ReactionSticker
)

// Reaction is one person's current Tapback on a message part.
type Reaction struct {
	Type     ReactionType
	Emoji    string
	Sender   string
	HandleID int64
	IsFromMe bool
	Part     int // zero-based body part; -1 means the whole app bubble
}

func (r Reaction) Label() string {
	switch r.Type {
	case ReactionLove:
		return "❤️"
	case ReactionLike:
		return "👍"
	case ReactionDislike:
		return "👎"
	case ReactionLaugh:
		return "😂"
	case ReactionEmphasis:
		return "‼️"
	case ReactionQuestion:
		return "❓"
	case ReactionEmoji:
		if r.Emoji != "" {
			return r.Emoji
		}
		return "Emoji"
	case ReactionSticker:
		return "Sticker"
	default:
		return "Unknown reaction"
	}
}

type messageSchema map[string]bool

func readMessageSchema(db *sql.DB) (messageSchema, error) {
	rows, err := db.Query("SELECT name FROM pragma_table_info('message')")
	if err != nil {
		return nil, fmt.Errorf("read message schema: %w", err)
	}
	defer rows.Close()
	schema := make(messageSchema)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read message column: %w", err)
		}
		schema[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(schema) == 0 {
		return nil, fmt.Errorf("message table is missing")
	}
	return schema, nil
}

func (s messageSchema) column(alias, name string) string {
	if s[name] {
		return alias + "." + name
	}
	return "NULL"
}

func (s messageSchema) reactionPredicate(alias string) string {
	if !s["associated_message_type"] {
		return "0"
	}
	column := "COALESCE(" + alias + ".associated_message_type, 0)"
	return fmt.Sprintf("(%s BETWEEN 2000 AND 2007 OR %s BETWEEN 3000 AND 3007)", column, column)
}

func isReactionType(value int64) bool {
	return value >= 2000 && value <= 2007 || value >= 3000 && value <= 3007
}

func parseReactionTarget(target string) (string, int, error) {
	lower := strings.ToLower(target)
	switch {
	case strings.HasPrefix(lower, "bp:"):
		if len(target) > 3 {
			return target[3:], -1, nil
		}
	case strings.HasPrefix(lower, "p:"):
		partText, guid, found := strings.Cut(target[2:], "/")
		part, err := strconv.Atoi(partText)
		if found && err == nil && part >= 0 && guid != "" {
			return guid, part, nil
		}
	default:
		if target != "" {
			return target, 0, nil
		}
	}
	return "", 0, fmt.Errorf("invalid reaction target")
}

type reactionKey struct {
	handleID int64
	fromMe   bool
	part     int
}

func applyReaction(state map[reactionKey]Reaction, reaction Reaction, removed bool) {
	key := reactionKey{reaction.HandleID, reaction.IsFromMe, reaction.Part}
	if reaction.IsFromMe {
		key.handleID = 0
	}
	if removed {
		current, found := state[key]
		// Removing an old Like must not clear the Love that replaced it.
		if found && current.Type == reaction.Type &&
			(reaction.Type != ReactionEmoji || reaction.Emoji == "" || reaction.Emoji == current.Emoji) {
			delete(state, key)
		}
		return
	}
	state[key] = reaction
}

func getReactionsForMessages(db *sql.DB, schema messageSchema, messages []Message) (map[int64][]Reaction, error) {
	if len(messages) == 0 || !schema["guid"] || !schema["associated_message_guid"] || !schema["associated_message_type"] {
		return nil, nil
	}
	targets := make(map[string][]int64)
	var placeholders []string
	var args []any
	for _, message := range messages {
		if message.GUID == "" {
			continue
		}
		key := strings.ToLower(message.GUID)
		if len(targets[key]) == 0 {
			placeholders = append(placeholders, "?")
			args = append(args, message.GUID)
		}
		targets[key] = append(targets[key], message.MessageID)
	}
	if len(args) == 0 {
		return nil, nil
	}
	// Read every matching reaction, not just rows inside the history window.
	// Normalize targets in one scan instead of scanning reactions once per message.
	query := fmt.Sprintf(`
		SELECT r.associated_message_guid, r.associated_message_type,
			r.handle_id, r.is_from_me, h.id, %s
		FROM message r
		LEFT JOIN handle h ON r.handle_id = h.ROWID
		WHERE %s AND (CASE
			WHEN LOWER(SUBSTR(r.associated_message_guid, 1, 3)) = 'bp:' THEN SUBSTR(r.associated_message_guid, 4)
			WHEN LOWER(SUBSTR(r.associated_message_guid, 1, 2)) = 'p:' THEN SUBSTR(r.associated_message_guid, INSTR(r.associated_message_guid, '/') + 1)
			ELSE r.associated_message_guid
		END) COLLATE NOCASE IN (%s)
		ORDER BY r.date ASC, r.ROWID ASC
	`, schema.column("r", "associated_message_emoji"), schema.reactionPredicate("r"), strings.Join(placeholders, ","))
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[int64]map[reactionKey]Reaction)
	for rows.Next() {
		var typeValue int64
		var target string
		var handleID, fromMe sql.NullInt64
		var sender, emoji sql.NullString
		if err := rows.Scan(&target, &typeValue, &handleID, &fromMe, &sender, &emoji); err != nil {
			return nil, fmt.Errorf("read reaction: %w", err)
		}
		guid, part, err := parseReactionTarget(target)
		if err != nil {
			return nil, fmt.Errorf("read reaction: %w", err)
		}
		removed := typeValue >= 3000
		if removed {
			typeValue -= 1000
		}
		reaction := Reaction{
			Type: ReactionType(typeValue), Emoji: emoji.String, HandleID: handleID.Int64,
			IsFromMe: fromMe.Int64 == 1, Part: part,
		}
		reaction.Sender = ResolveSender(reaction.IsFromMe, sender.String)
		for _, messageID := range targets[strings.ToLower(guid)] {
			if states[messageID] == nil {
				states[messageID] = make(map[reactionKey]Reaction)
			}
			applyReaction(states[messageID], reaction, removed)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make(map[int64][]Reaction, len(states))
	for messageID, state := range states {
		for _, reaction := range state {
			result[messageID] = append(result[messageID], reaction)
		}
		sort.Slice(result[messageID], func(i, j int) bool {
			a, b := result[messageID][i], result[messageID][j]
			if a.Part != b.Part {
				return a.Part < b.Part
			}
			if a.Type != b.Type {
				return a.Type < b.Type
			}
			if a.Sender != b.Sender {
				return a.Sender < b.Sender
			}
			if a.IsFromMe != b.IsFromMe {
				return !a.IsFromMe
			}
			return a.HandleID < b.HandleID
		})
	}
	return result, nil
}

package database

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/danewalton/imessage-cli/internal/testutil"
)

func TestReactionTargets(t *testing.T) {
	for _, tc := range []struct {
		target string
		guid   string
		part   int
		bad    bool
	}{
		{"message-guid", "message-guid", 0, false},
		{"p:0/message-guid", "message-guid", 0, false},
		{"p:12/message-guid", "message-guid", 12, false},
		{"bp:message-guid", "message-guid", -1, false},
		{"BP:MESSAGE-GUID", "MESSAGE-GUID", -1, false},
		{"", "", 0, true}, {"bp:", "", 0, true},
		{"p:bad/message-guid", "", 0, true}, {"p:-1/message-guid", "", 0, true},
		{"p:0/", "", 0, true}, {"p:0", "", 0, true},
	} {
		guid, part, err := parseReactionTarget(tc.target)
		if guid != tc.guid || part != tc.part || (err != nil) != tc.bad {
			t.Errorf("%q: got %q,%d,%v", tc.target, guid, part, err)
		}
	}
}

func TestReactionLabels(t *testing.T) {
	for kind, want := range map[ReactionType]string{
		ReactionLove: "❤️", ReactionLike: "👍", ReactionDislike: "👎", ReactionLaugh: "😂",
		ReactionEmphasis: "‼️", ReactionQuestion: "❓", ReactionEmoji: "Emoji", ReactionSticker: "Sticker",
	} {
		if got := (Reaction{Type: kind}).Label(); got != want {
			t.Errorf("label for %d: got %q, want %q", kind, got, want)
		}
	}
	if got := (Reaction{Type: ReactionEmoji, Emoji: "👩‍💻"}).Label(); got != "👩‍💻" {
		t.Errorf("custom emoji changed: %q", got)
	}
}

func testReactionSnapshots(t *testing.T, db *sql.DB) {
	testutil.ReactionSchema(t, db)
	t.Cleanup(func() {
		testutil.Exec(t, db, "DELETE FROM chat_message_join WHERE message_id >= 10; DELETE FROM message WHERE ROWID >= 10")
	})
	testutil.Exec(t, db, `
			INSERT INTO message (ROWID, text, date, is_from_me, is_read, service, handle_id,
				guid, associated_message_guid, associated_message_type, associated_message_emoji) VALUES
				(10,'reaction control',600,0,1,'iMessage',1,'event-10','p:0/message-1',2001,NULL),
				(11,'reaction control',700,0,1,'iMessage',1,'event-11','p:0/message-1',2000,NULL),
				(12,'reaction control',800,0,1,'iMessage',1,'event-12','p:0/message-1',3001,NULL),
				(13,'reaction control',900,0,1,'iMessage',4,'event-13','message-1',2001,NULL),
				(14,'reaction control',901,0,1,'iMessage',4,'event-14','p:0/MESSAGE-1',2001,NULL),
				(15,'reaction control',902,0,1,'iMessage',4,'event-15','p:0/message-1',3001,NULL),
				(16,'reaction control',1000,1,1,'iMessage',NULL,'event-16','p:1/message-1',2005,NULL),
				(17,'reaction control',1001,1,1,'iMessage',NULL,'event-17','p:0/message-1',3005,NULL),
				(18,'reaction control',1100,0,1,'iMessage',4,'event-18','p:0/message-1',2006,'🚀'),
				(19,'reaction control',1200,0,1,'iMessage',4,'event-19','p:0/message-1',3006,'🌈'),
				(20,'reaction control',1250,0,1,'iMessage',1,'event-20','bp:message-1',2001,NULL),
				(21,'reaction control',1300,0,1,'iMessage',4,'event-21','p:0/message-2',2001,NULL),
				(22,'reaction control',1400,0,1,'iMessage',4,'event-22','p:0/message-5',2001,NULL),
				(23,'reaction control',1400,0,1,'iMessage',4,'event-23','p:0/message-5',3001,NULL),
				(24,'reply body',50,0,1,'iMessage',1,'reply-guid','p:0/message-1',100,NULL),
				(25,NULL,1500,0,1,'iMessage',1,'orphan-event','p:0/missing-parent',2000,NULL);
			INSERT INTO chat_message_join SELECT 1, ROWID FROM message WHERE ROWID BETWEEN 10 AND 24;
			UPDATE message SET associated_message_type=NULL WHERE ROWID=4;
		`)

	msgs, err := GetMessages(1, "", 10)
	if err != nil || len(msgs) != 6 {
		t.Fatalf("reaction rows consumed history: %+v, %v", msgs, err)
	}
	parent := msgs[1]
	if parent.MessageID != 1 || parent.GUID != "message-1" || len(parent.Reactions) != 4 {
		t.Fatalf("parent snapshot: %+v", parent)
	}
	for i, want := range []struct {
		kind ReactionType
		part int
	}{
		{ReactionLike, -1}, {ReactionLove, 0}, {ReactionEmoji, 0}, {ReactionQuestion, 1},
	} {
		got := parent.Reactions[i]
		if got.Type != want.kind || got.Part != want.part {
			t.Errorf("reaction %d: %+v, want %d on part %d", i, got, want.kind, want.part)
		}
	}
	if parent.Reactions[2].Emoji != "🚀" || parent.Reactions[3].Sender != "Me" || !parent.Reactions[3].IsFromMe {
		t.Errorf("lost custom emoji or outgoing identity: %+v", parent.Reactions)
	}
	if len(msgs[2].Reactions) != 1 || len(msgs[5].Reactions) != 0 {
		t.Errorf("cross-message or equal-timestamp removal error: %+v", msgs)
	}
	recent, err := GetMessages(0, "notalex@example.test", 2)
	if err != nil || len(recent) != 2 || recent[0].MessageID != 4 || recent[1].MessageID != 5 {
		t.Fatalf("limit or identifier path: %+v, %v", recent, err)
	}
	if results, err := SearchMessages("reaction control", 10); err != nil || len(results) != 0 {
		t.Fatalf("reaction events appeared in search: %+v, %v", results, err)
	}
	if results, err := SearchMessages("reply body", 1); err != nil || len(results) != 1 || results[0].MessageID != 24 {
		t.Fatalf("non-reaction association hidden: %+v, %v", results, err)
	}
	events, err := GetMessagesSince(24)
	if err != nil || len(events) != 1 || !events[0].IsReaction || events[0].Text != "" || events[0].ChatID != 0 {
		t.Fatalf("orphan event must not block polling or become an attachment: %+v, %v", events, err)
	}

	t.Run("optional emoji column", func(t *testing.T) {
		testutil.Exec(t, db, "ALTER TABLE message RENAME COLUMN associated_message_emoji TO hidden_emoji")
		defer testutil.Exec(t, db, "ALTER TABLE message RENAME COLUMN hidden_emoji TO associated_message_emoji")
		testutil.Exec(t, db, `
			INSERT INTO message (ROWID, date, is_from_me, is_read, handle_id, associated_message_guid, associated_message_type)
				VALUES (30,1600,0,1,4,'p:0/message-2',2006)
		`)
		defer testutil.Exec(t, db, "DELETE FROM message WHERE ROWID=30")
		msgs, err := GetMessages(1, "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs[2].Reactions) != 1 || msgs[2].Reactions[0].Type != ReactionEmoji || msgs[2].Reactions[0].Label() != "Emoji" {
			t.Errorf("missing emoji metadata must have an explicit label: %+v", msgs[2])
		}
	})

	t.Run("GUID matching is literal", func(t *testing.T) {
		testutil.Exec(t, db, `
				UPDATE message SET guid='message_%_5' WHERE ROWID=5;
				INSERT INTO message (ROWID, date, is_from_me, is_read, handle_id, associated_message_guid, associated_message_type)
					VALUES (30,1600,0,1,1,'p:0/message_XY_5',2001), (31,1601,0,1,4,'p:0/message_%_5',2000);
			`)
		defer testutil.Exec(t, db, "UPDATE message SET guid='message-5' WHERE ROWID=5; DELETE FROM message WHERE ROWID>=30")
		msgs, err := GetMessages(1, "", 2)
		if err != nil || len(msgs) != 2 || len(msgs[1].Reactions) != 1 || msgs[1].Reactions[0].Type != ReactionLove {
			t.Fatalf("GUID wildcard matched another target: %+v, %v", msgs, err)
		}
	})

	t.Run("reactions older than the history window", func(t *testing.T) {
		testutil.Exec(t, db, `
			INSERT INTO message (ROWID, date, is_from_me, is_read, handle_id, associated_message_guid, associated_message_type)
				VALUES (30,1,0,1,1,'p:0/message-4',2001)
		`)
		defer testutil.Exec(t, db, "DELETE FROM message WHERE ROWID=30")
		msgs, err := GetMessages(1, "", 2)
		if err != nil || len(msgs) != 2 || msgs[0].MessageID != 4 || len(msgs[0].Reactions) != 1 || msgs[0].Reactions[0].Type != ReactionLike {
			t.Fatalf("older reaction dropped from the snapshot: %+v, %v", msgs, err)
		}
	})

	t.Run("invalid target is surfaced", func(t *testing.T) {
		testutil.Exec(t, db, `
				INSERT INTO message (ROWID, date, is_from_me, is_read, handle_id, associated_message_guid, associated_message_type)
					VALUES (30,1600,0,1,1,'p:bad/message-1',2001)
			`)
		defer testutil.Exec(t, db, "DELETE FROM message WHERE ROWID=30")
		if _, err := GetMessages(1, "", 10); err == nil || !strings.Contains(err.Error(), "reaction target") {
			t.Fatalf("invalid association hidden: %v", err)
		}
	})
}

func TestReactionStateUsesActorAndPart(t *testing.T) {
	state := make(map[reactionKey]Reaction)
	like := Reaction{Type: ReactionLike, HandleID: 1, Sender: "Same name"}
	applyReaction(state, like, false)
	applyReaction(state, like, false)
	if len(state) != 1 {
		t.Fatal("duplicate event counted twice")
	}
	love := like
	love.Type = ReactionLove
	applyReaction(state, love, false)
	applyReaction(state, like, true)
	if state[reactionKey{handleID: 1}].Type != ReactionLove {
		t.Fatal("obsolete removal cleared the replacement")
	}
	other := like
	other.HandleID = 2
	applyReaction(state, other, false)
	part := like
	part.Part = 1
	applyReaction(state, part, false)
	if len(state) != 3 {
		t.Fatal("display names or message parts merged distinct reactions")
	}
	applyReaction(state, love, true)
	if len(state) != 2 {
		t.Fatal("removal affected another person or part")
	}

	emoji := Reaction{Type: ReactionEmoji, HandleID: 3, Emoji: "🚀"}
	applyReaction(state, emoji, false)
	oldEmoji := emoji
	oldEmoji.Emoji = "🌈"
	applyReaction(state, oldEmoji, true)
	if state[reactionKey{handleID: 3}].Emoji != "🚀" {
		t.Fatal("removal of a different emoji cleared the current one")
	}
	applyReaction(state, emoji, true)
	if _, found := state[reactionKey{handleID: 3}]; found {
		t.Fatal("custom emoji removal failed")
	}

	mine := Reaction{Type: ReactionQuestion, IsFromMe: true, HandleID: 1}
	applyReaction(state, mine, false)
	mine.HandleID = 2
	applyReaction(state, mine, false)
	if state[reactionKey{fromMe: true}].HandleID != 2 {
		t.Fatal("outgoing handle variants did not replace my reaction")
	}
	applyReaction(state, mine, true)
	if _, found := state[reactionKey{fromMe: true}]; found {
		t.Fatal("outgoing reaction removal failed")
	}
}

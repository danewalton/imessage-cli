package database

import (
	"strings"
	"testing"

	"github.com/danewalton/imessage-cli/internal/testutil"
)

func TestExtractAttributedBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"empty", nil, ""},
		{"fallback", []byte("synthetic fallback content"), "synthetic fallback content"},
		{"unicode", []byte("Hello 世界 😀"), "Hello 世界 😀"},
		{"stream", []byte("\x00streamtypedNSStringmeetingNSDictionary"), "meeting"},
		{"structured", []byte("NSString" + strings.Repeat("\x00", 6) + "meeting" + strings.Repeat("\x00", 12) + "NSDictionaryNSNumber"), "meeting"},
		{"artifacts", []byte("bplist\x00NSString\x00NSNumber"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractTextFromAttributedBody(tc.body); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDatabaseRegressions(t *testing.T) {
	db := testutil.Database(t)
	t.Cleanup(CloseDB)
	testutil.Exec(t, db, `
		INSERT INTO handle VALUES
			(1, 'notalex@example.test', 'iMessage'),
			(2, 'a_b@example.test', 'iMessage'),
			(3, '+12025550101', 'iMessage'),
			(4, 'blue@example.test', 'iMessage');
		INSERT INTO chat VALUES
			(1, 'iMessage;-;notalex@example.test', 'notalex@example.test', 'Amber', 'iMessage'),
			(2, 'iMessage;-;a_b@example.test', 'a_b@example.test', 'Underscore', 'iMessage'),
			(3, 'iMessage;-;+12025550101', '+12025550101', 'Phone', 'iMessage'),
			(4, 'iMessage;+;chat-synthetic', 'chat-synthetic', 'Group', 'iMessage');
		INSERT INTO chat_handle_join VALUES (1,1), (2,2), (3,3), (4,1), (4,4);
		INSERT INTO message VALUES
			(1, 'plain MEETING', NULL, 100, 0, 1, 'iMessage', 1),
			(2, NULL, CAST(char(0) || 'streamtypedNSStringmeeting' AS BLOB), 200, 0, 1, 'iMessage', 1),
			(3, '100% literal', NULL, 300, 0, 1, 'iMessage', 1),
			(4, 'plain text', CAST('streamtypedNSStringmeeting' AS BLOB), 400, 0, 1, 'iMessage', 1),
			(5, 'ÉCOLE', NULL, 500, 0, 1, 'iMessage', 1);
		INSERT INTO chat_message_join VALUES (1,1), (1,2), (1,3), (1,4), (1,5);
	`)

	t.Run("exact identifiers exclude groups and substring matches", func(t *testing.T) {
		for _, tc := range []struct {
			input string
			id    int64
		}{
			{"alex@example.test", 0}, {"alex", 0}, {"acb@example.test", 0},
			{"a_b@example.test", 2}, {"NOTALEX@example.test", 1},
			{"+1 (202) 555-0101", 3}, {"2025550101", 3},
			{"%example.test", 0},
		} {
			conv, err := GetContactByIdentifier(tc.input)
			if err != nil {
				t.Fatalf("%q: %v", tc.input, err)
			}
			if tc.id == 0 {
				if conv != nil {
					t.Errorf("%q incorrectly resolved %+v", tc.input, conv)
				}
			} else if conv == nil || conv.ChatID != tc.id || conv.ChatGUID == "" {
				t.Errorf("%q: got %+v, want ID %d with GUID", tc.input, conv, tc.id)
			}
		}
	})

	t.Run("ambiguity is an error", func(t *testing.T) {
		testutil.Exec(t, db, `INSERT INTO chat VALUES (6,'SMS;-;notalex@example.test','notalex@example.test','SMS Amber','SMS'); INSERT INTO chat_handle_join VALUES (6,1)`)
		defer testutil.Exec(t, db, "DELETE FROM chat_handle_join WHERE chat_id=6; DELETE FROM chat WHERE ROWID=6")
		_, err := GetContactByIdentifier("notalex@example.test")
		if err == nil || !strings.Contains(err.Error(), "multiple conversations") {
			t.Fatalf("got %v, want explicit ambiguity", err)
		}
	})

	t.Run("invalid phone normalization is rejected", func(t *testing.T) {
		for _, input := range []string{"", "+", "1+2025550101", "++12025550101"} {
			if _, err := GetContactByIdentifier(input); err == nil {
				t.Errorf("invalid recipient %q accepted", input)
			}
		}
	})

	t.Run("stable group identity", func(t *testing.T) {
		conv, err := GetConversationByID(4)
		if err != nil || conv.ChatGUID != "iMessage;+;chat-synthetic" || len(conv.Participants) != 2 {
			t.Fatalf("group identity: %+v, %v", conv, err)
		}
		if _, err := GetConversationByID(99); err == nil {
			t.Fatal("missing chat must return an error")
		}
	})

	t.Run("search decoded text before limiting", func(t *testing.T) {
		for _, tc := range []struct {
			query string
			limit int
			ids   []int64
		}{
			{"meeting", 20, []int64{2, 1}},
			{"meeting", 1, []int64{2}},
			{"%", 20, []int64{3}},
			{"école", 20, []int64{5}},
			{"meeting", 0, nil},
		} {
			msgs, err := SearchMessages(tc.query, tc.limit)
			if err != nil || len(msgs) != len(tc.ids) {
				t.Fatalf("%q: got %+v, %v; want %v", tc.query, msgs, err, tc.ids)
			}
			for i, id := range tc.ids {
				if msgs[i].MessageID != id {
					t.Errorf("result %d got %d, want %d", i, msgs[i].MessageID, id)
				}
			}
		}
		msgs, err := GetMessages(1, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		if msgs[1].MessageID != 2 || msgs[1].Text != "meeting" {
			t.Fatalf("read/search mismatch: %+v", msgs)
		}
		events, err := GetMessagesSince(0)
		if err != nil || len(events) != 5 || events[1].Text != "meeting" || events[1].IsReaction {
			t.Fatalf("legacy schema incremental decoding: %+v, %v", events, err)
		}
	})

	t.Run("reaction snapshots", func(t *testing.T) {
		testReactionSnapshots(t, db)
	})

	t.Run("attachment query errors propagate", func(t *testing.T) {
		testutil.Exec(t, db, "ALTER TABLE attachment RENAME TO hidden_attachment")
		defer testutil.Exec(t, db, "ALTER TABLE hidden_attachment RENAME TO attachment")
		_, err := GetMessages(1, "", 10)
		if err == nil || !strings.Contains(err.Error(), "attachments") {
			t.Fatalf("attachment failure suppressed: %v", err)
		}
	})

	t.Run("query errors are not empty data", func(t *testing.T) {
		testutil.Exec(t, db, "ALTER TABLE message RENAME TO hidden_message")
		if _, err := GetConversations(10); err == nil {
			t.Error("conversation query failure suppressed")
		}
		if _, err := GetMessages(1, "", 10); err == nil {
			t.Error("message query failure suppressed")
		}
		if _, err := SearchMessages("meeting", 10); err == nil {
			t.Error("search failure suppressed")
		}
	})
}

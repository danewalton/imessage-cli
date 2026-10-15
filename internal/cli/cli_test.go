package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/danewalton/imessage-cli/internal/database"
	"github.com/danewalton/imessage-cli/internal/testutil"
)

func TestConversationIndex(t *testing.T) {
	for _, tc := range []struct {
		input     string
		index     int
		isIndex   bool
		wantError bool
	}{
		{"1", 1, true, false}, {"#2", 2, true, false},
		{"+12025550101", 0, false, false}, {"2025550101", 0, false, false},
		{"amber@example.test", 0, false, false}, {"chat:42", 0, false, false},
		{"0", 0, true, true}, {"#oops", 0, true, true}, {"#", 0, true, true},
	} {
		index, isIndex, err := conversationIndex(tc.input)
		if index != tc.index || isIndex != tc.isIndex || (err != nil) != tc.wantError {
			t.Errorf("%q: got %d,%v,%v", tc.input, index, isIndex, err)
		}
	}
}

func TestCLIRegressions(t *testing.T) {
	db := testutil.Database(t)
	t.Cleanup(database.CloseDB)
	logPath := testutil.AppleScript(t)
	testutil.Exec(t, db, `
		INSERT INTO handle VALUES (1,'+12025550101','iMessage'), (2,'blue@example.test','iMessage');
		INSERT INTO chat VALUES
			(1,'iMessage;-;+12025550101','+12025550101','Amber','iMessage'),
			(2,'iMessage;+;chat-synthetic','chat-synthetic','Group','iMessage');
		INSERT INTO chat_handle_join VALUES (1,1),(2,1),(2,2);
		INSERT INTO message VALUES
			(1,'phone message',NULL,200,0,1,'iMessage',1),
			(2,'group message',NULL,100,0,1,'iMessage',2);
		INSERT INTO chat_message_join VALUES (1,1),(2,2);
	`)

	t.Run("documented phone commands work", func(t *testing.T) {
		out := testutil.Output(t, func() {
			if err := cmdRead("+12025550101", 30); err != nil {
				t.Error(err)
			}
		})
		if !strings.Contains(out, "phone message") || strings.Contains(out, "Invalid conversation") {
			t.Fatalf("phone read: %s", out)
		}
		testutil.Input(t, "quit\n", func() {
			testutil.Output(t, func() {
				if err := cmdChat("+12025550101"); err != nil {
					t.Error(err)
				}
			})
		})
	})

	t.Run("reply tips use stable targets", func(t *testing.T) {
		for _, reference := range []string{"1", "#1", "chat:1"} {
			out := testutil.Output(t, func() {
				if err := cmdRead(reference, 30); err != nil {
					t.Error(err)
				}
			})

			t.Run("list exposes stable references", func(t *testing.T) {
				out := testutil.Output(t, func() {
					if err := cmdList(20); err != nil {
						t.Error(err)
					}
				})
				if !strings.Contains(out, "chat:1") || !strings.Contains(out, "chat:2") {
					t.Fatalf("stable references missing: %s", out)
				}
			})
			if !strings.Contains(out, "imessage send 'chat:1'") || strings.Contains(out, `imessage send "1"`) {
				t.Fatalf("incorrect reply tip for %s: %s", reference, out)
			}
		}
	})

	t.Run("groups keep their chat identity", func(t *testing.T) {
		testutil.Output(t, func() {
			if err := cmdSend("chat:2", "group reply", true); err != nil {
				t.Error(err)
			}
		})
		testutil.Input(t, "interactive group reply\nquit\n", func() {
			testutil.Output(t, func() {
				if err := cmdChat("2"); err != nil {
					t.Error(err)
				}
			})
		})
		data, err := os.ReadFile(logPath)
		if err != nil || strings.Count(string(data), `chat id "iMessage;+;chat-synthetic"`) != 2 || strings.Contains(string(data), "buddy ") {
			t.Fatalf("group routing: %v\n%s", err, data)
		}
	})

	t.Run("unknown addresses are never rewritten", func(t *testing.T) {
		target, err := resolveConversation("new@example.test")
		if err != nil || target.conversation != nil || target.recipient != "new@example.test" {
			t.Fatalf("unknown recipient: %+v, %v", target, err)
		}
	})

	t.Run("read and chat show reaction snapshots", func(t *testing.T) {
		testutil.ReactionSchema(t, db)
		testutil.Exec(t, db, `
			INSERT INTO message (ROWID, text, date, is_from_me, is_read, handle_id,
				associated_message_guid, associated_message_type)
				VALUES (3,'reaction control',300,0,1,2,'p:0/message-1',2001);
			INSERT INTO chat_message_join VALUES (1,3)
		`)
		defer testutil.Exec(t, db, "DELETE FROM chat_message_join WHERE message_id=3; DELETE FROM message WHERE ROWID=3")
		out := testutil.Output(t, func() {
			if err := cmdRead("chat:1", 1); err != nil {
				t.Error(err)
			}
		})
		if !strings.Contains(out, "phone message") || !strings.Contains(out, "👍 blue@example.test") || strings.Contains(out, "reaction control") {
			t.Errorf("read reaction presentation: %s", out)
		}
		testutil.Input(t, "quit\n", func() {
			out := testutil.Output(t, func() {
				if err := cmdChat("chat:1"); err != nil {
					t.Error(err)
				}
			})
			if !strings.Contains(out, "👍 blue@example.test") || strings.Contains(out, "reaction control") {
				t.Errorf("chat reaction presentation: %s", out)
			}
		})
	})

	t.Run("status propagates database errors", func(t *testing.T) {
		testutil.Exec(t, db, "ALTER TABLE message RENAME TO hidden_message")
		var statusErr error
		out := testutil.Output(t, func() { statusErr = statusCmd.RunE(statusCmd, nil) })
		if statusErr == nil || strings.Contains(out, "Conversations: 0") || strings.Contains(out, "Unread messages: 0") {
			t.Fatalf("status reported success: %v\n%s", statusErr, out)
		}
		if err := readCmd.RunE(readCmd, []string{"+12025550101"}); err == nil {
			t.Fatal("read command suppressed a database error")
		}
		if err := listCmd.RunE(listCmd, nil); err == nil {
			t.Error("list command suppressed a database error")
		}
		if err := searchCmd.RunE(searchCmd, []string{"meeting"}); err == nil {
			t.Error("search command suppressed a database error")
		}
	})
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b$()"); got != "'a'\\''b$()'" {
		t.Fatalf("unsafe reply argument: %s", got)
	}
}

package watcher

import (
	"testing"
	"time"

	"github.com/danewalton/imessage-cli/internal/database"
	"github.com/danewalton/imessage-cli/internal/testutil"
)

func TestWatcherRegressions(t *testing.T) {
	db := testutil.Database(t)
	t.Cleanup(database.CloseDB)
	testutil.Exec(t, db, `
		INSERT INTO handle VALUES (1,'amber@example.test','iMessage');
		INSERT INTO chat VALUES (1,'iMessage;-;amber@example.test','amber@example.test','Amber','iMessage');
		INSERT INTO chat_handle_join VALUES (1,1);
		INSERT INTO message VALUES (1,'hello',NULL,100,0,1,'iMessage',1);
		INSERT INTO chat_message_join VALUES (1,1);
	`)
	w := NewMessageWatcher(time.Hour)
	convs, err := w.GetConversations(10)
	if err != nil || len(convs) != 1 || convs[0].ChatGUID != "iMessage;-;amber@example.test" {
		t.Fatalf("lost chat identity: %+v, %v", convs, err)
	}
	if msgs, err := w.GetMessages(1, 10); err != nil || len(msgs) != 1 {
		t.Fatalf("message load: %+v, %v", msgs, err)
	}
	t.Run("reaction events and deletion refresh", func(t *testing.T) {
		testutil.ReactionSchema(t, db)
		defer testutil.Exec(t, db, "DELETE FROM chat_message_join WHERE message_id=2; DELETE FROM message WHERE ROWID=2")
		newMessages := make(chan []Message, 4)
		conversationChanges := make(chan []Conversation, 4)
		w.OnNewMessages(func(msgs []Message) { newMessages <- msgs })
		w.OnConversationsUpdated(func(convs []Conversation) { conversationChanges <- convs })
		w.lastMessageID.Store(1)
		w.lastMtime.Store(w.getDBMtime())
		time.Sleep(time.Millisecond)
		testutil.Exec(t, db, `
			INSERT INTO message (ROWID, date, is_from_me, is_read, handle_id, guid,
				associated_message_guid, associated_message_type)
				VALUES (2,200,0,1,1,'event-2','p:0/message-1',2001);
			INSERT INTO chat_message_join VALUES (1,2)
		`)
		w.poll()
		select {
		case msgs := <-newMessages:
			if len(msgs) != 1 || !msgs[0].IsReaction || msgs[0].ChatID != 1 || msgs[0].Text != "" {
				t.Errorf("reaction event lost: %+v", msgs)
			}
		case <-time.After(time.Second):
			t.Fatal("reaction-only insert did not notify")
		}
		select {
		case <-conversationChanges:
		case <-time.After(time.Second):
			t.Fatal("reaction insert did not refresh conversations")
		}
		msgs, err := w.GetMessages(1, 10)
		if err != nil || len(msgs) != 1 || msgs[0].GUID != "message-1" || len(msgs[0].Reactions) != 1 {
			t.Fatalf("snapshot forwarding: %+v, %v", msgs, err)
		}
		time.Sleep(time.Millisecond)
		testutil.Exec(t, db, "UPDATE message SET associated_message_type=2000 WHERE ROWID=2")
		w.poll()
		select {
		case <-conversationChanges:
		case <-time.After(time.Second):
			t.Fatal("in-place reaction replacement did not notify")
		}
		if msgs, err := w.GetMessages(1, 10); err != nil || len(msgs) != 1 || len(msgs[0].Reactions) != 1 || msgs[0].Reactions[0].Type != database.ReactionLove {
			t.Fatalf("in-place replacement retained old state: %+v, %v", msgs, err)
		}
		time.Sleep(time.Millisecond)
		testutil.Exec(t, db, "DELETE FROM chat_message_join WHERE message_id=2; DELETE FROM message WHERE ROWID=2")
		w.poll()
		select {
		case <-conversationChanges:
		case <-time.After(time.Second):
			t.Fatal("reaction deletion without a new ROWID did not notify")
		}
		if msgs, err := w.GetMessages(1, 10); err != nil || len(msgs) != 1 || len(msgs[0].Reactions) != 0 {
			t.Fatalf("deleted reaction retained: %+v, %v", msgs, err)
		}
		w.lastMessageID.Store(0)
		w.lastMtime.Store(0)
	})
	testutil.Exec(t, db, "ALTER TABLE chat_message_join RENAME TO hidden_join")
	if _, err := w.GetConversations(10); err == nil {
		t.Error("conversation error suppressed")
	}
	if _, err := w.GetMessages(1, 10); err == nil {
		t.Error("message error suppressed")
	}
	if _, err := w.GetNewMessages(0); err == nil {
		t.Error("new-message error suppressed")
	}
	errors := make(chan error, 2)
	w.OnError(func(err error) { errors <- err })
	w.poll()
	select {
	case <-errors:
	case <-time.After(time.Second):
		t.Fatal("poll error not surfaced")
	}
	if w.lastMessageID.Load() != 0 {
		t.Fatal("failed message read advanced the cursor")
	}
	w.lastMessageID.Store(1)
	w.poll()
	select {
	case <-errors:
	case <-time.After(time.Second):
		t.Fatal("conversation poll error not surfaced")
	}
	if w.lastMtime.Load() != 0 {
		t.Fatal("failed refresh advanced the modification cursor")
	}
}

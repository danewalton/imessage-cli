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

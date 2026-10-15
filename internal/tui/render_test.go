package tui

import (
	"strings"
	"testing"

	"github.com/danewalton/imessage-cli/internal/database"
	"github.com/danewalton/imessage-cli/internal/watcher"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestReactionGraphemesUseFullCellWidth(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reaction database.Reaction
		width    int
	}{
		{"love", database.Reaction{Type: database.ReactionLove}, 2},
		{"emphasis", database.Reaction{Type: database.ReactionEmphasis}, 2},
		{"like", database.Reaction{Type: database.ReactionLike}, 2},
		{"flag", database.Reaction{Type: database.ReactionEmoji, Emoji: "🇺🇸"}, 2},
		{"skin tone", database.Reaction{Type: database.ReactionEmoji, Emoji: "👍🏽"}, 2},
		{"joined emoji", database.Reaction{Type: database.ReactionEmoji, Emoji: "👩‍💻"}, 2},
		{"combining accent", database.Reaction{Type: database.ReactionEmoji, Emoji: "e\u0301"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			screen := tcell.NewSimulationScreen("")
			if err := screen.Init(); err != nil {
				t.Fatal(err)
			}
			defer screen.Fini()
			screen.SetSize(80, 8)
			tc.reaction.Sender = "Blue"
			var text strings.Builder
			NewMessagesTUI().formatMessageLine(&text, watcher.Message{
				Text: "original message", Sender: "Amber",
				Reactions: []database.Reaction{tc.reaction},
			})
			view := tview.NewTextView().SetDynamicColors(true)
			view.SetRect(0, 0, 80, 8)
			view.SetText(text.String())
			view.Draw(screen)
			main, combining, _, width := screen.GetContent(14, 1)
			if got := string(append([]rune{main}, combining...)); got != tc.reaction.Label() {
				t.Errorf("reaction changed: got %q, want %q", got, tc.reaction.Label())
			}
			if width != tc.width {
				t.Errorf("reaction cell width: got %d, want %d", width, tc.width)
			}
		})
	}
}

func TestReactionPresentationChangesUpdateCellWidth(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	for _, tc := range []struct {
		text  string
		width int
	}{
		{"❤", 1}, {"❤️", 2}, {"❤︎", 1}, {"❤️", 2},
	} {
		tview.Print(screen, tc.text, 0, 0, 8, tview.AlignLeft, tcell.ColorWhite)
		main, combining, _, width := screen.GetContent(0, 0)
		if got := string(append([]rune{main}, combining...)); got != tc.text || width != tc.width {
			t.Errorf("after changing presentation to %q: got %q width %d, want width %d", tc.text, got, width, tc.width)
		}
	}
}

func TestReplacingReactionMessageClearsItsCells(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(80, 8)
	view := tview.NewTextView().SetDynamicColors(true)
	view.SetRect(0, 0, 80, 8)

	var text strings.Builder
	NewMessagesTUI().formatMessageLine(&text, watcher.Message{
		Text: "first conversation", Sender: "Amber",
		Attachments: []watcher.Attachment{{Filename: "one.png"}},
		Reactions: []database.Reaction{
			{Type: database.ReactionLove, Sender: "Blue", Part: 0},
			{Type: database.ReactionLove, Sender: "Me", Part: 0},
		},
	})
	for i := 0; i < 3; i++ {
		screen.Clear()
		view.SetText(text.String())
		view.Draw(screen)
		screen.Show()
		_, _, _, width := screen.GetContent(14, 2)
		if width != 2 {
			t.Errorf("heart width before replacement: got %d, want 2", width)
		}

		screen.Clear()
		view.SetText("second conversation\n")
		view.Draw(screen)
		screen.Show()
		cells, columns, _ := screen.GetContents()
		for x := 0; x < columns; x++ {
			if got := string(cells[2*columns+x].Runes); got != " " {
				t.Fatalf("reaction cell %d left %q after replacement", x, got)
			}
		}
	}
}

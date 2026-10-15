// Package cli provides the command-line interface for iMessage CLI.
package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danewalton/imessage-cli/internal/database"
	"github.com/danewalton/imessage-cli/internal/sender"
	"github.com/danewalton/imessage-cli/internal/tui"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[91m"
	colorGreen  = "\033[92m"
	colorYellow = "\033[93m"
	colorBlue   = "\033[94m"
	colorCyan   = "\033[96m"
)

func colored(text string, colors ...string) string {
	if !isTerminal() {
		return text
	}
	return strings.Join(colors, "") + text + colorReset
}

func isTerminal() bool {
	fileInfo, _ := os.Stdout.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

func formatDate(t *time.Time) string {
	if t == nil {
		return "Unknown"
	}

	now := time.Now()
	diff := now.Sub(*t)

	if diff.Hours() < 24 {
		return t.Format("03:04 PM")
	} else if diff.Hours() < 48 {
		return "Yesterday " + t.Format("03:04 PM")
	} else if diff.Hours() < 168 { // 7 days
		return t.Format("Monday 03:04 PM")
	}
	return t.Format("2006-01-02 03:04 PM")
}

func truncate(text string, maxLen int) string {
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.TrimSpace(text)
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-3] + "..."
}

var rootCmd = &cobra.Command{
	Use:   "imessage",
	Short: "Read and respond to iMessages from the command line",
	Long: `iMessage CLI - A command-line tool for reading and sending iMessages on macOS.

Examples:
  imessage tui                     Launch interactive TUI with live updates
  imessage list                    List recent conversations
  imessage read 1                  Read messages from conversation #1
  imessage read chat:42            Read a stable conversation ID
  imessage read "+1234567890"      Read messages from a phone number
  imessage send "+1234567890" "Hi" Send a message
  imessage send chat:42 "Hi"       Reply to an existing chat or group
  imessage chat 1                  Start interactive chat with conversation #1
  imessage search "meeting"        Search for messages containing "meeting"

Note: This tool requires macOS with Messages configured and proper permissions.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmdList(20)
	},
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "l"},
	Short:   "List recent conversations and stable chat IDs",
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		return cmdList(limit)
	},
}

var readCmd = &cobra.Command{
	Use:     "read <conversation>",
	Aliases: []string{"r", "view"},
	Short:   "Read messages from a conversation",
	Long:    "Read messages using a list index, phone number, email, or stable chat:<id> reference.",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		return cmdRead(args[0], limit)
	},
}

var sendCmd = &cobra.Command{
	Use:     "send <recipient> <message>",
	Aliases: []string{"s"},
	Short:   "Send a message",
	Long:    "Send to a phone number or email, or use chat:<id> for an existing direct or group conversation.",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		return cmdSend(args[0], args[1], yes)
	},
}

var chatCmd = &cobra.Command{
	Use:     "chat <contact>",
	Aliases: []string{"c"},
	Short:   "Interactive chat mode",
	Long:    "Start interactive chat using a list index, phone number, email, or stable chat:<id> reference.",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmdChat(args[0])
	},
}

var searchCmd = &cobra.Command{
	Use:     "search <query>",
	Aliases: []string{"find", "grep"},
	Short:   "Search messages",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		return cmdSearch(args[0], limit)
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status and statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmdStatus()
	},
}

var tuiCmd = &cobra.Command{
	Use:     "tui",
	Aliases: []string{"ui", "watch"},
	Short:   "Launch interactive TUI with live updates",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Read debug flag from the command's flags to avoid init-time cycles
		debug, _ := cmd.Flags().GetBool("debug")
		if debug {
			if err := tui.RunWithDebug(true, ""); err != nil {
				return fmt.Errorf("launch TUI: %w", err)
			}
			return nil
		}

		if err := tui.Run(); err != nil {
			return fmt.Errorf("launch TUI: %w", err)
		}
		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("imessage version %s\n", version)
	},
}

func init() {
	listCmd.Flags().IntP("limit", "n", 20, "Number of conversations to show")
	readCmd.Flags().IntP("limit", "n", 30, "Number of messages to show")
	sendCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
	searchCmd.Flags().IntP("limit", "n", 20, "Maximum results")

	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(readCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(statusCmd)
	// Add tui command with debug flag
	tuiCmd.Flags().BoolP("debug", "d", false, "Enable private TUI debug logging in the user cache directory")
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(versionCmd)
}

// Execute runs the root command.
func Execute() error {
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	return rootCmd.Execute()
}

func cmdList(limit int) error {
	conversations, err := database.GetConversations(limit)
	if err != nil {
		return fmt.Errorf("load conversations: %w", err)
	}

	if len(conversations) == 0 {
		fmt.Println("No conversations found.")
		return nil
	}

	header := fmt.Sprintf("\n%-4s %-12s %-30s %-20s %-10s", "#", "Chat ID", "Contact", "Last Message", "Service")
	fmt.Println(colored(header, colorBold, colorCyan))
	fmt.Println(strings.Repeat("-", 83))

	for i, conv := range conversations {
		name := truncate(conv.DisplayName, 28)
		dateStr := formatDate(conv.LastMessageDate)
		service := conv.Service
		if service == "" {
			service = "iMessage"
		}

		serviceColor := colorBlue
		if strings.Contains(service, "SMS") {
			serviceColor = colorGreen
		}

		fmt.Printf("%-4d %-12s %-30s %-20s %s\n", i+1, fmt.Sprintf("chat:%d", conv.ChatID), name, dateStr, colored(service, serviceColor))
	}

	unread, err := database.GetUnreadCount()
	if err != nil {
		return fmt.Errorf("load unread count: %w", err)
	}
	if unread > 0 {
		fmt.Println(colored(fmt.Sprintf("\n📬 %d unread message(s)", unread), colorYellow, colorBold))
	}

	fmt.Println(colored("\nTip: Use 'imessage read <number>' or a stable 'chat:<id>' reference", colorDim))
	return nil
}

type conversationTarget struct {
	conversation *database.Conversation
	recipient    string
}

func (target conversationTarget) name() string {
	if target.conversation != nil && target.conversation.DisplayName != "" {
		return target.conversation.DisplayName
	}
	return target.recipient
}

func (target conversationTarget) messages(limit int) ([]database.Message, error) {
	if target.conversation != nil {
		return database.GetMessages(target.conversation.ChatID, "", limit)
	}
	return database.GetMessages(0, target.recipient, limit)
}

func (target conversationTarget) send(message string) error {
	if target.conversation != nil {
		return sender.SendToConversation(*target.conversation, message)
	}
	return sender.SendMessage(target.recipient, message)
}

func conversationIndex(reference string) (int, bool, error) {
	explicit := strings.HasPrefix(reference, "#")
	number := strings.TrimPrefix(reference, "#")
	if !explicit && (len(number) == 0 || len(number) > 3) {
		return 0, false, nil
	}
	for _, r := range number {
		if r < '0' || r > '9' {
			if explicit {
				return 0, true, fmt.Errorf("invalid conversation index %q", reference)
			}
			return 0, false, nil
		}
	}
	index, err := strconv.Atoi(number)
	if err != nil || index < 1 {
		return 0, true, fmt.Errorf("invalid conversation index %q", reference)
	}
	return index, true, nil
}

func resolveConversation(reference string) (conversationTarget, error) {
	if strings.HasPrefix(reference, "chat:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(reference, "chat:"), 10, 64)
		if err != nil || id <= 0 {
			return conversationTarget{}, fmt.Errorf("invalid chat reference %q", reference)
		}
		conv, err := database.GetConversationByID(id)
		return conversationTarget{conversation: conv}, err
	}
	index, isIndex, err := conversationIndex(reference)
	if err != nil {
		return conversationTarget{}, err
	}
	if isIndex {
		convs, err := database.GetConversations(100)
		if err != nil {
			return conversationTarget{}, err
		}
		if index > len(convs) {
			return conversationTarget{}, fmt.Errorf("invalid conversation number; use 1-%d", len(convs))
		}
		return conversationTarget{conversation: &convs[index-1]}, nil
	}
	conv, err := database.GetContactByIdentifier(reference)
	return conversationTarget{conversation: conv, recipient: reference}, err
}

func cmdRead(conversation string, limit int) error {
	target, err := resolveConversation(conversation)
	if err != nil {
		return err
	}
	messages, err := target.messages(limit)
	if err != nil {
		return fmt.Errorf("read messages: %w", err)
	}

	if len(messages) == 0 {
		fmt.Printf("No messages found for %s\n", target.name())
		return nil
	}

	fmt.Println(colored(fmt.Sprintf("\n📱 Messages with %s", target.name()), colorBold, colorCyan))
	fmt.Println(strings.Repeat("-", 60))

	for _, msg := range messages {
		dateStr := formatDate(msg.Date)
		text := msg.Text
		if text == "" {
			text = "[No text content]"
		}

		if msg.IsFromMe {
			fmt.Printf("\n%58s\n", colored(dateStr, colorDim))
			fmt.Printf("%10s %s\n", colored("Me:", colorGreen, colorBold), text)
		} else {
			fmt.Printf("\n%s\n", colored(dateStr, colorDim))
			fmt.Printf("%s %s\n", colored(msg.Sender+":", colorBlue, colorBold), text)
		}
		printReactions(msg)
	}

	fmt.Println("\n" + strings.Repeat("-", 60))

	replyTarget := target.recipient
	if target.conversation != nil {
		replyTarget = fmt.Sprintf("chat:%d", target.conversation.ChatID)
	}
	fmt.Println(colored(fmt.Sprintf("Reply: imessage send %s 'your message'", shellQuote(replyTarget)), colorDim))
	return nil
}

func printReactions(msg database.Message) {
	for _, reaction := range msg.Reactions {
		part := ""
		if reaction.Part < 0 {
			part = " (bubble)"
		} else if reaction.Part > 0 || len(msg.Attachments) > 0 {
			part = fmt.Sprintf(" (part %d)", reaction.Part+1)
		}
		fmt.Printf("  %s\n", colored(reaction.Label()+" "+reaction.Sender+part, colorDim))
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func cmdSend(recipient, message string, skipConfirm bool) error {
	target := conversationTarget{recipient: recipient}
	if strings.HasPrefix(recipient, "chat:") {
		var err error
		target, err = resolveConversation(recipient)
		if err != nil {
			return err
		}
	}
	if !skipConfirm {
		destination := recipient
		if target.conversation != nil {
			destination = fmt.Sprintf("%s (%s)", target.name(), recipient)
		}
		fmt.Printf("%s %s\n", colored("Sending to:", colorBold), destination)
		fmt.Printf("%s %s\n", colored("Message:", colorBold), message)

		reader := bufio.NewReader(os.Stdin)
		fmt.Print(colored("\nSend this message? [y/N] ", colorYellow))
		confirm, _ := reader.ReadString('\n')
		confirm = strings.TrimSpace(strings.ToLower(confirm))

		if confirm != "y" && confirm != "yes" {
			fmt.Println("Message cancelled.")
			return nil
		}
	}

	fmt.Println("Sending message...")

	err := target.send(message)
	if err != nil {
		fmt.Println(colored("\nMake sure:", colorYellow))
		fmt.Println("  1. Messages app is configured and signed in")
		fmt.Println("  2. You've granted Terminal/SSH full disk access in System Preferences")
		fmt.Println("  3. The recipient is a valid phone number, email, or chat:<id> reference")
		return err
	}

	fmt.Println(colored("✓ Message sent successfully!", colorGreen, colorBold))
	return nil
}

func cmdChat(contact string) error {
	target, err := resolveConversation(contact)
	if err != nil {
		return err
	}

	fmt.Println(colored(fmt.Sprintf("\n💬 Chat with %s", target.name()), colorBold, colorCyan))
	fmt.Println(colored("Type your message and press Enter to send. Type 'quit' or Ctrl+C to exit.", colorDim))
	fmt.Println(colored("Type 'refresh' or 'r' to reload messages.", colorDim))
	fmt.Println(strings.Repeat("-", 60))

	showMessages := func() error {
		messages, err := target.messages(10)
		if err != nil {
			return fmt.Errorf("load messages: %w", err)
		}

		for _, msg := range messages {
			dateStr := formatDate(msg.Date)
			text := msg.Text
			if text == "" {
				text = ""
			}
			if msg.IsFromMe {
				fmt.Printf("  %s %s\n", colored(fmt.Sprintf("[%s] Me:", dateStr), colorGreen), text)
			} else {
				fmt.Printf("  %s %s\n", colored(fmt.Sprintf("[%s] %s:", dateStr, msg.Sender), colorBlue), text)
			}
			printReactions(msg)
		}
		fmt.Println()
		return nil
	}

	if err := showMessages(); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(colored("You: ", colorGreen, colorBold))
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("\nGoodbye!")
			break
		}

		input = strings.TrimSpace(input)

		switch strings.ToLower(input) {
		case "quit", "exit", "q":
			fmt.Println("Goodbye!")
			return nil
		case "refresh", "r":
			fmt.Println(colored("\n--- Refreshing ---\n", colorDim))
			if err := showMessages(); err != nil {
				fmt.Fprintln(os.Stderr, "Error refreshing:", err)
			}
			continue
		case "":
			continue
		}

		err = target.send(input)
		if err != nil {
			fmt.Println(colored(fmt.Sprintf("  ✗ Failed to send: %v", err), colorRed))
		} else {
			fmt.Println(colored("  ✓ Sent", colorDim))
		}
	}
	return nil
}

func cmdSearch(query string, limit int) error {
	results, err := database.SearchMessages(query, limit)
	if err != nil {
		return fmt.Errorf("search messages: %w", err)
	}

	if len(results) == 0 {
		fmt.Printf("No messages found matching '%s'\n", query)
		return nil
	}

	fmt.Println(colored(fmt.Sprintf("\nSearch results for '%s':", query), colorBold, colorCyan))
	fmt.Println(strings.Repeat("-", 70))

	for _, msg := range results {
		dateStr := formatDate(msg.Date)
		chat := truncate(msg.ChatName, 20)
		senderName := "Me"
		if !msg.IsFromMe {
			senderName = truncate(msg.Sender, 15)
		}
		text := truncate(msg.Text, 40)

		fmt.Printf("%-20s %s %-17s %s\n",
			dateStr,
			colored(fmt.Sprintf("%-22s", chat), colorCyan),
			colored(senderName, colorYellow),
			text)
	}

	fmt.Printf("\nFound %d message(s)\n", len(results))
	return nil
}

func cmdStatus() error {
	fmt.Println(colored("\n📊 iMessage CLI Status", colorBold, colorCyan))
	fmt.Println(strings.Repeat("-", 40))

	// Check database access
	dbPath := database.GetDBPath()
	if _, err := os.Stat(dbPath); err == nil {
		fmt.Printf("%s Database found: %s\n", colored("✓", colorGreen), dbPath)
	} else {
		return fmt.Errorf("cannot access database %s: %w (check Full Disk Access)", dbPath, err)
	}

	// Check Messages app
	if sender.CheckMessagesRunning() {
		fmt.Printf("%s Messages app is running\n", colored("✓", colorGreen))
	} else {
		fmt.Printf("%s Messages app is not running\n", colored("○", colorYellow))
	}

	// Show stats
	conversations, err := database.GetConversations(1000)
	if err != nil {
		return fmt.Errorf("load conversation statistics: %w (check Full Disk Access)", err)
	}
	unread, err := database.GetUnreadCount()
	if err != nil {
		return fmt.Errorf("load unread statistics: %w", err)
	}

	fmt.Println("\n📈 Statistics:")
	fmt.Printf("   Conversations: %d\n", len(conversations))
	fmt.Printf("   Unread messages: %d\n", unread)
	fmt.Println()
	return nil
}

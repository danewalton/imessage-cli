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
  imessage read "+1234567890"      Read messages from a phone number
  imessage send "+1234567890" "Hi" Send a message
  imessage chat 1                  Start interactive chat with conversation #1
  imessage search "meeting"        Search for messages containing "meeting"

Note: This tool requires macOS with Messages configured and proper permissions.`,
	Run: func(cmd *cobra.Command, args []string) {
		cmdList(20)
	},
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls", "l"},
	Short:   "List recent conversations",
	Run: func(cmd *cobra.Command, args []string) {
		limit, _ := cmd.Flags().GetInt("limit")
		cmdList(limit)
	},
}

var readCmd = &cobra.Command{
	Use:     "read <conversation>",
	Aliases: []string{"r", "view"},
	Short:   "Read messages from a conversation",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		limit, _ := cmd.Flags().GetInt("limit")
		cmdRead(args[0], limit)
	},
}

var sendCmd = &cobra.Command{
	Use:     "send <recipient> <message>",
	Aliases: []string{"s"},
	Short:   "Send a message",
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		cmdSend(args[0], args[1], yes)
	},
}

var chatCmd = &cobra.Command{
	Use:     "chat <contact>",
	Aliases: []string{"c"},
	Short:   "Interactive chat mode",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cmdChat(args[0])
	},
}

var searchCmd = &cobra.Command{
	Use:     "search <query>",
	Aliases: []string{"find", "grep"},
	Short:   "Search messages",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		limit, _ := cmd.Flags().GetInt("limit")
		cmdSearch(args[0], limit)
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status and statistics",
	Run: func(cmd *cobra.Command, args []string) {
		cmdStatus()
	},
}

var tuiCmd = &cobra.Command{
	Use:     "tui",
	Aliases: []string{"ui", "watch"},
	Short:   "Launch interactive TUI with live updates",
	Run: func(cmd *cobra.Command, args []string) {
		// Read debug flag from the command's flags to avoid init-time cycles
		debug, _ := cmd.Flags().GetBool("debug")
		if debug {
			if err := tui.RunWithDebug(true, ""); err != nil {
				fmt.Println(colored(fmt.Sprintf("Error launching TUI: %v", err), colorRed))
				os.Exit(1)
			}
			return
		}

		if err := tui.Run(); err != nil {
			fmt.Println(colored(fmt.Sprintf("Error launching TUI: %v", err), colorRed))
			os.Exit(1)
		}
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
	tuiCmd.Flags().BoolP("debug", "d", false, "Enable TUI debug logging to /tmp/imessage-tui.log")
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(versionCmd)
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func cmdList(limit int) {
	conversations, err := database.GetConversations(limit)
	if err != nil {
		fmt.Println(colored(fmt.Sprintf("Error: %v", err), colorRed))
		os.Exit(1)
	}

	if len(conversations) == 0 {
		fmt.Println("No conversations found.")
		return
	}

	header := fmt.Sprintf("\n%-4s %-30s %-20s %-10s", "#", "Contact", "Last Message", "Service")
	fmt.Println(colored(header, colorBold, colorCyan))
	fmt.Println(strings.Repeat("-", 70))

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

		fmt.Printf("%-4d %-30s %-20s %s\n", i+1, name, dateStr, colored(service, serviceColor))
	}

	unread, _ := database.GetUnreadCount()
	if unread > 0 {
		fmt.Println(colored(fmt.Sprintf("\n📬 %d unread message(s)", unread), colorYellow, colorBold))
	}

	fmt.Println(colored("\nTip: Use 'imessage read <number>' to view messages from a conversation", colorDim))
}

func cmdRead(conversation string, limit int) {
	conversations, err := database.GetConversations(100)
	if err != nil {
		fmt.Println(colored(fmt.Sprintf("Error: %v", err), colorRed))
		os.Exit(1)
	}

	var chatID int64
	var chatIdentifier string
	var chatName string

	if idx, err := strconv.Atoi(conversation); err == nil {
		// User provided a number from the list
		idx--
		if idx >= 0 && idx < len(conversations) {
			conv := conversations[idx]
			chatID = conv.ChatID
			chatName = conv.DisplayName
		} else {
			fmt.Println(colored(fmt.Sprintf("Invalid conversation number. Use 1-%d", len(conversations)), colorRed))
			os.Exit(1)
		}
	} else {
		// User provided a phone number or identifier
		chatIdentifier = conversation
		contact, _ := database.GetContactByIdentifier(chatIdentifier)
		if contact != nil {
			if contact.ChatIdentifier != "" {
				chatIdentifier = contact.ChatIdentifier
			}
			if contact.DisplayName != "" {
				chatName = contact.DisplayName
			} else {
				chatName = chatIdentifier
			}
		} else {
			chatName = chatIdentifier
		}
	}

	var messages []database.Message
	if chatID > 0 {
		messages, err = database.GetMessages(chatID, "", limit)
	} else {
		messages, err = database.GetMessages(0, chatIdentifier, limit)
	}

	if err != nil {
		fmt.Println(colored(fmt.Sprintf("Error reading messages: %v", err), colorRed))
		os.Exit(1)
	}

	if len(messages) == 0 {
		fmt.Printf("No messages found for %s\n", chatName)
		return
	}

	fmt.Println(colored(fmt.Sprintf("\n📱 Messages with %s", chatName), colorBold, colorCyan))
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
	}

	fmt.Println("\n" + strings.Repeat("-", 60))

	replyTarget := chatIdentifier
	if replyTarget == "" {
		replyTarget = conversation
	}
	fmt.Println(colored(fmt.Sprintf("Reply: imessage send \"%s\" \"your message\"", replyTarget), colorDim))
}

func cmdSend(recipient, message string, skipConfirm bool) {
	if !skipConfirm {
		fmt.Printf("%s %s\n", colored("Sending to:", colorBold), recipient)
		fmt.Printf("%s %s\n", colored("Message:", colorBold), message)

		reader := bufio.NewReader(os.Stdin)
		fmt.Print(colored("\nSend this message? [y/N] ", colorYellow))
		confirm, _ := reader.ReadString('\n')
		confirm = strings.TrimSpace(strings.ToLower(confirm))

		if confirm != "y" && confirm != "yes" {
			fmt.Println("Message cancelled.")
			return
		}
	}

	fmt.Println("Sending message...")

	err := sender.SendMessage(recipient, message)
	if err != nil {
		fmt.Println(colored(fmt.Sprintf("Error: %v", err), colorRed))
		fmt.Println(colored("\nMake sure:", colorYellow))
		fmt.Println("  1. Messages app is configured and signed in")
		fmt.Println("  2. You've granted Terminal/SSH full disk access in System Preferences")
		fmt.Println("  3. The recipient is a valid phone number or email")
		os.Exit(1)
	}

	fmt.Println(colored("✓ Message sent successfully!", colorGreen, colorBold))
}

func cmdChat(contact string) {
	conversations, err := database.GetConversations(100)
	if err != nil {
		fmt.Println(colored(fmt.Sprintf("Error: %v", err), colorRed))
		os.Exit(1)
	}

	var chatID int64
	var chatIdentifier string
	var chatName string

	if idx, err := strconv.Atoi(contact); err == nil {
		idx--
		if idx >= 0 && idx < len(conversations) {
			conv := conversations[idx]
			chatID = conv.ChatID
			chatIdentifier = conv.ChatIdentifier
			chatName = conv.DisplayName
		} else {
			fmt.Println(colored("Invalid conversation number", colorRed))
			os.Exit(1)
		}
	} else {
		chatIdentifier = contact
		c, _ := database.GetContactByIdentifier(chatIdentifier)
		if c != nil {
			if c.ChatIdentifier != "" {
				chatIdentifier = c.ChatIdentifier
			}
			if c.DisplayName != "" {
				chatName = c.DisplayName
			} else {
				chatName = chatIdentifier
			}
		} else {
			chatName = chatIdentifier
		}
	}

	fmt.Println(colored(fmt.Sprintf("\n💬 Chat with %s", chatName), colorBold, colorCyan))
	fmt.Println(colored("Type your message and press Enter to send. Type 'quit' or Ctrl+C to exit.", colorDim))
	fmt.Println(colored("Type 'refresh' or 'r' to reload messages.", colorDim))
	fmt.Println(strings.Repeat("-", 60))

	showMessages := func() {
		var messages []database.Message
		if chatID > 0 {
			messages, _ = database.GetMessages(chatID, "", 10)
		} else {
			messages, _ = database.GetMessages(0, chatIdentifier, 10)
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
		}
		fmt.Println()
	}

	showMessages()

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
			return
		case "refresh", "r":
			fmt.Println(colored("\n--- Refreshing ---\n", colorDim))
			showMessages()
			continue
		case "":
			continue
		}

		err = sender.SendMessage(chatIdentifier, input)
		if err != nil {
			fmt.Println(colored("  ✗ Failed to send", colorRed))
		} else {
			fmt.Println(colored("  ✓ Sent", colorDim))
		}
	}
}

func cmdSearch(query string, limit int) {
	results, err := database.SearchMessages(query, limit)
	if err != nil {
		fmt.Println(colored(fmt.Sprintf("Error searching: %v", err), colorRed))
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Printf("No messages found matching '%s'\n", query)
		return
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
}

func cmdStatus() {
	fmt.Println(colored("\n📊 iMessage CLI Status", colorBold, colorCyan))
	fmt.Println(strings.Repeat("-", 40))

	// Check database access
	dbPath := database.GetDBPath()
	if _, err := os.Stat(dbPath); err == nil {
		fmt.Printf("%s Database found: %s\n", colored("✓", colorGreen), dbPath)
	} else {
		fmt.Printf("%s Database not found: %s\n", colored("✗", colorRed), dbPath)
	}

	// Check Messages app
	if sender.CheckMessagesRunning() {
		fmt.Printf("%s Messages app is running\n", colored("✓", colorGreen))
	} else {
		fmt.Printf("%s Messages app is not running\n", colored("○", colorYellow))
	}

	// Show stats
	conversations, _ := database.GetConversations(1000)
	unread, _ := database.GetUnreadCount()

	fmt.Println("\n📈 Statistics:")
	fmt.Printf("   Conversations: %d\n", len(conversations))
	fmt.Printf("   Unread messages: %d\n", unread)
	fmt.Println()
}

func cmdTUI() {
	if err := tui.Run(); err != nil {
		fmt.Println(colored(fmt.Sprintf("Error launching TUI: %v", err), colorRed))
		os.Exit(1)
	}
}

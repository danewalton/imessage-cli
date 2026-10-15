# Code Review - usability-correctness

## Review Overview

| Item | Result |
|------|--------|
| Scope | Entire tracked repository at f6fa6ab |
| Focus | Correctness, reliability, and usability |
| Recommendations | 13 defects: 5 High, 8 Medium; 7 optional UX improvements |
| Review passes | A: correctness review; B: workflow review. Both used harness defaults; model identities were not exposed, so distinct model families are not claimed. |
| Scope Test Coverage | 0.0% of statements in all six source packages, measured with `go test -cover ./...` before adding temporary review probes |
| Not in review focus | Architecture-only assessment, dependency audit, observability-only assessment, and a separate test-coverage review |
| Runtime limits | Review and synthetic reproductions ran on Linux. Native Messages.app delivery and macOS permission prompts were not exercised. |

### Overall Assessment

The highest-risk issues are not cosmetic: live updates and stale loads can change a draft's destination, identifier lookup can select another person, and a second send can freeze the interface. A message-body fallback also deterministically panics. Fix recipient identity and event-loop ownership before adding more UI features.

Twelve defects were reproduced with synthetic SQLite data, a simulated terminal, or a stub sender. Group-send routing is supported by the source call chain but still needs native macOS qualification. No production source was changed.

### Principal-Engineer Focus Areas

| Area | Why it matters | Findings |
|------|----------------|----------|
| Conversation selection and asynchronous loading | A list highlight, composer, and sender must all refer to the same stable chat ID, even during reordering and delayed completion. | 1.1, 1.2 |
| Recipient and group targeting | User-entered addresses must not become substring matches, list positions, or opaque group identifiers passed as buddy addresses. | 1.3, 1.5, 1.6, 1.7 |
| Event-loop and send lifecycle | UI callbacks must not wait for themselves; send failures must not overwrite later drafts. | 7.1, 7.2 |

## Prioritized Recommendations

**Start here:** 1.1, 1.2, 1.3, 7.1, 1.4.

All findings are Core review findings. A and B refer to the independent review passes; Orchestrator identifies a defect discovered during reproduction.

| ID | Severity | Area | Recommendation | Attribution | Evidence |
|----|----------|------|----------------|-------------|----------|
| 1.1 | High | Correctness | Preserve selection and drafts by chat ID during live reordering | A + B | Reproduced |
| 1.2 | High | Correctness | Discard stale message loads instead of letting them change selection | A + B | Reproduced |
| 1.3 | High | Correctness | Match recipient identifiers exactly; reject ambiguity | A + B | Reproduced |
| 7.1 | High | Reliability | Do not call blocking QueueUpdateDraw from the event loop | A + B | Reproduced |
| 1.4 | High | Correctness | Fix the invalid attributed-body fallback regex | Orchestrator | Reproduced |
| 1.5 | Medium | Correctness | Distinguish international phone numbers from list indexes | A + B | Reproduced |
| 1.6 | Medium | Correctness | Generate reply tips from the resolved destination | A | Reproduced |
| 1.7 | Medium | Correctness | Send selected groups through an explicit chat-target API | A + B | Source verified; native delivery unverified |
| 1.8 | Medium | Correctness | Escape message text, names, and filenames before adding TUI markup | A + B | Reproduced |
| 1.9 | Medium | Correctness | Search decoded attributed-body text rather than binary blobs as text | A | Reproduced |
| 6.1 | Medium | Security | Do not log composer characters; keep debug logs private | B | Character capture reproduced; file exposure depends on permissions |
| 7.2 | Medium | Reliability | Keep failed sends separate from the current draft | A + B | Reproduced |
| 7.3 | Medium | Reliability | Surface database errors instead of reporting empty data | B | Status failure reproduced; TUI error conversion source verified |

## Detailed Findings

### 1. Correctness

#### 1.1 Live reordering can change a draft's recipient - High - Core - A + B

**Location:** `internal/tui/tui.go:245-253,749-778`.

The conversation refresh saves a list index, clears the list, and restores that index in a newly ordered list. Selection callbacks update selectedChatID, including the callback fired when the first item is added. The composer remains unchanged.

**Failure:** Select Amber, start a draft, then let Blue receive a message and move above Amber. The same highlighted position now represents Blue, and sending uses Blue's identifier. A simulated terminal reproduced selectedChatID changing from 1 to 2 while retaining `"draft for Amber"`.

**Recommendation:** Preserve selection by chat ID, suppress intermediate callbacks during rebuilding, and update the list and its backing data together on the event loop. Associate drafts with chat IDs.

#### 1.2 Stale loads overwrite the current conversation - High - Core - A + B

**Location:** `internal/tui/tui.go:245-253,487-525,573-578`.

loadMessages assigns both messages and selectedChatID after fetching, without checking whether its request is still current. Selection triggers concurrent loads, and the post-send path reloads the original chat after a delay.

**Failure:** Select Amber, then Blue; a late Amber load replaces Blue's messages and the send destination. The list can still highlight Blue. A simulation reproduced an old Amber load changing the selected ID and title after Blue was selected.

**Recommendation:** Selection should be changed only by selection actions. Tag loads with chat ID and request generation, and apply results on the event loop only if both still match. A post-send refresh must not reopen an inactive chat.

#### 1.3 Identifier lookup can choose another person's chat - High - Core - A + B

**Location:** `internal/database/database.go:531-570`; `internal/cli/cli.go:386-392,456-460`.

Lookup uses `%identifier%` and `%normalized%` LIKE patterns with LIMIT 1. The interactive chat command replaces the supplied recipient with the result. Matching is therefore a recipient-routing decision, not merely a display-name convenience.

**Failure:** With only `notalex@example.test` in the fixture, asking for `alex@example.test` returns the other person's chat. SQL wildcard characters such as `_` can also broaden a match. A normalization that produces an empty string can produce a match-all pattern.

**Recommendation:** Use exact email matching and explicitly supported phone variants. Reject ambiguity, and never replace a destination with an unchecked substring match.

#### 1.4 Attributed-body fallback always panics when reached - High - Core - Orchestrator

**Location:** `internal/database/database.go:203`.

The fallback calls `regexp.MustCompile` with the raw pattern `[\x20-\x7E\u00A0-\uFFFF]{3,}`. Go's regexp parser does not support these `\u` escapes.

**Failure:** `ExtractTextFromAttributedBody([]byte("synthetic fallback content"))` panics with `invalid escape sequence: \u`. Any nonempty body that reaches this fallback can crash a reading command or a TUI loading goroutine. This is a deterministic runtime failure, not malformed-input speculation.

**Recommendation:** Use supported code-point escapes such as `\x{00A0}` and `\x{FFFF}`, compile the constant expression once, and add tests that reach every extraction strategy.

#### 1.5 Phone numbers are mistaken for conversation indexes - Medium - Core - A + B

**Location:** `internal/cli/cli.go:259-272,375-387`.

read and chat treat any successful strconv.Atoi result as a list index. A leading `+` is valid for Atoi.

**Failure:** Both `read "+12025550101"` and `chat "+12025550101"` reported an invalid conversation number in the synthetic CLI fixture. These are the same input forms recommended in the README.

**Recommendation:** Reserve `+`-prefixed input for phone numbers. Make index syntax explicit enough to avoid ambiguity with unprefixed numeric addresses.

#### 1.6 Indexed reads print an unusable reply command - Medium - Core - A

**Location:** `internal/cli/cli.go:259-265,326-351`.

The indexed read path resolves chat ID and name, but not the identifier used by its reply tip. The tip falls back to the original argument. send does not resolve list indexes.

**Failure:** Reading fixture conversation 1 prints `imessage send "1" "your message"` although its recipient is `notalex@example.test`.

**Recommendation:** Generate the tip from the resolved target. Use a stable chat-target command for groups rather than an individual-address command.

#### 1.7 Group replies go through an individual-recipient API - Medium - Core - A + B

**Location:** `internal/tui/tui.go:538-565`; `internal/cli/cli.go:375-381,456`; `internal/sender/sender.go:13-77`.

Both interfaces pass Conversation.ChatIdentifier to SendMessage. All three sending strategies interpret that string as a buddy or participant address. An opaque group-chat identifier is not carried as a chat target. SendToGroup exists, but these call paths do not use it.

**Failure:** Selecting a group and replying generates an individual-recipient operation using the group's identifier. The source mismatch is clear; the native Messages.app outcome was not exercised.

**Recommendation:** Add an explicit send-to-chat path using stable identity. Do not replace it with nonunique display-name matching or sending to one participant. Until supported, reject group sends with a clear explanation.

#### 1.8 Literal message content becomes formatting - Medium - Core - A + B

**Location:** `internal/tui/tui.go:164-169,800-819`.

The message view enables dynamic colors and interpolates message bodies, sender names, and attachment filenames directly into tagged text.

**Failure:** Formatting `"literal [red] token"` removes the literal `[red]` token and changes styling. The reproduction confirmed the token was absent from the displayed plain text.

**Recommendation:** Escape external content with tview.Escape while leaving application-owned style tags unchanged.

#### 1.9 Search misses readable attributed-body messages - Medium - Core - A

**Location:** `internal/database/database.go:440-460`.

Search uses CAST(attributedBody AS TEXT) LIKE rather than matching the extracted message text. Binary serialization can contain NUL bytes, which terminate SQLite's LIKE input.

**Failure:** A synthetic body beginning with NUL followed by `streamtypedNSStringmeeting` displays `"meeting"` through GetMessages, but SearchMessages returns no match. The fixture deliberately uses the second extraction strategy to avoid conflating this defect with the fallback panic.

**Recommendation:** Search decoded text. Use bounded candidate processing or a separately maintained extracted-text index; a binary substring search alone is not a complete decoder.

### 6. Security

#### 6.1 Debug logging captures drafts and can expose them to local users - Medium - Core - B

**Location:** `internal/tui/tui.go:112-121,281-295`.

Input capture logs each printable rune before checking whether the input field has focus. Debug mode opens `/tmp/imessage-tui.log` for append with creation mode 0644.

**Failure:** The synthetic input `"TEST-SECRET"` appeared character by character in diagnostic logging, even with the composer focused. With a conventional 022 umask, a newly created log is readable by other local users. Existing file permissions and effective umask can change that exposure; they were not measured on macOS.

**Recommendation:** Do not record composer characters. Use a private per-user directory and owner-only permissions, including when reopening an existing log.

### 7. Reliability & Resilience

#### 7.1 Rejected sends deadlock the event loop - High - Core - A + B

**Location:** `internal/tui/tui.go:265-270,529-554`.

The input completion callback runs on the UI event loop. If a send is already pending, sendMessage calls QueueUpdateDraw, which waits for that same event loop to execute the update. The no-conversation branch does the same.

**Failure:** Submit a second message while the first is pending. The rejection path never returns, so navigation and drawing stop. A simulated event-loop reproduction confirmed the block in an isolated subprocess.

**Recommendation:** Set status directly in event-loop callbacks. Make send acceptance explicit, preserve rejected drafts, and queue updates only from background workers.

#### 7.2 Failed sends overwrite a newer draft - Medium - Core - A + B

**Location:** `internal/tui/tui.go:265-273,557-570`.

Submission clears the composer immediately. A later failure restores the old text unconditionally, even if the user typed more or switched chats.

**Failure:** Submit `"first draft"`, then type `"second draft"` while a stub sender is pending. When the stub fails, the input becomes `"first draft"`, losing the newer text.

**Recommendation:** Retain pending and failed sends separately by chat ID. Restore only when the original chat is active and the composer has not changed; otherwise provide an explicit retry/recovery action.

#### 7.3 Database failures look like successful empty results - Medium - Core - B

**Location:** `internal/cli/cli.go:503-524`; `internal/watcher/watcher.go:149-176`; `internal/tui/tui.go:440-452`.

status ignores query errors. Watcher fetch wrappers also turn errors into nil slices, so the TUI cannot distinguish an empty account from denied access or query failure.

**Failure:** With a valid SQLite file lacking the message tables, status exits successfully and prints zero conversations and zero unread messages. This was reproduced. TUI conversion to an empty state follows directly from the source. Native Full Disk Access denial was not tested.

**Recommendation:** Preserve errors through the watcher API. Show a specific failed-load state and retain previously loaded data when refresh fails. status should report failed checks rather than valid-looking statistics.

## Optional UX Improvements

These are product suggestions, not additional confirmed defects.

| Opportunity | Suggested experience | Important tradeoff |
|-------------|----------------------|--------------------|
| Searchable conversation picker | `/` or a search field filters by contact, address, and group participants. Show identity and service when names collide. | Search must reach beyond the currently loaded 50 chats. |
| Composer tied to its conversation | Show the resolved recipient/group next to the input and retain separate drafts when switching chats. | Define when drafts expire and avoid retaining private text unnecessarily. |
| Visible send state and recovery | Show sending/failed messages in the transcript with an explicit retry action, rather than relying on a transient status bar. | Do not call a message delivered merely because AppleScript accepted it; avoid automatic retries after an uncertain outcome. |
| Multiline composing | Support paragraphs and pasted code, with a clear send shortcut such as Ctrl+Enter where the terminal supports it. | Keep single-line behavior familiar and ensure terminal key compatibility. |
| Older history and useful search results | Load older messages on demand, preserve the scroll anchor, and let a search result open its thread at the matching message. | Pagination adds query and view-state work; avoid unlimited loading. |
| Message-relative attachment preview | Choose an attachment from the message being viewed, rather than always opening the newest image; size previews to the terminal. | Large image decoding/conversion should stay off the UI event loop. |
| Contextual help and stable CLI references | `?` shows controls for the current panel; copyable commands use stable chat references instead of changing list positions. | Preserve familiar aliases while making addresses, indexes, and group targets unambiguous. |

## Reproduction Evidence and Coverage Summary

Before review probes, `go test -cover ./...` reported 0.0% for each of cmd/imessage, internal/cli, internal/database, internal/sender, internal/tui, and internal/watcher. `go vet ./...` completed without findings, and the CLI built successfully.

Temporary review probes intentionally asserted the existing defective behavior; their passing results confirm reproductions, not correctness. The final run was:

```sh
REVIEW_BINARY=/workspace/build/go/imessage-cli/9fd135b4-f649-4c8f-a027-4826f1e45fbc/imessage-review \
  go test -run '^TestReview' -v -count=1 -timeout=30s . ./internal/tui
```

All final probes passed. Their source is retained in this session's files directory as `review-probes/root_test.go` and `review-probes/tui_test.go`. The send-failure reproduction used a stub `osascript`, not Messages.app. The temporary probe files were removed from the repository after reproduction; the review binary was also removed and can be rebuilt with `go build -o <path> ./cmd/imessage`.

| Untested area in the original repository | Regression tests worth adding |
|-----------------------------------------|-------------------------------|
| Selection and loading | Reorder by new activity; preserve chat ID and draft; reversed load completion; switch during delayed post-send refresh |
| Recipient targeting | Exact emails, LIKE wildcard characters, formatted phone variants, ambiguous handles, direct versus group chat targets |
| Composer lifecycle | Second Enter during a pending send; no selected chat; failure after new typing; failure after switching chats |
| Message decoding and search | Every attributed-body extraction path; binary NULs; Unicode; readable text searchable; literal TUI tags |
| CLI workflows | README phone examples; indexed-read reply tips; stable references; explicit database failures |
| Diagnostics | No composer content in logs; owner-only log permissions; distinguish inaccessible databases from empty accounts |

## Model Cross-Reference Notes

Both independent passes agreed on the recipient-selection, stale-load, identifier-matching, phone parsing, group-routing, formatting, deadlock, and draft-loss issues. Pass A additionally found the reply-tip and binary-search issues; pass B additionally found debug logging and suppressed database errors.

The orchestrator found the invalid regex panic while attempting the binary-search reproduction. The initial generic fallback fixture exposed that panic, so the search fixture was changed to use a separate valid extraction path. The final evidence distinguishes both defects.

The first composer-loss probe did not render the input widget, which caused a dependency text-length artifact. After putting the input field in the simulated layout, the probe reproduced the exact overwrite described above. The discarded intermediate artifact is not a finding.

No source recommendations were accepted or waived by the user. No PR publication or CI gate applies to this whole-repository review.

## Accepted / Not Addressed

Not applicable: unattended continuation, with no user decisions to fix, defer, or waive findings.

## Review Context

- **Review Name:** usability-correctness
- **Report File:** code-reviews/usability-correctness-2026-10-15_0015.md
- **Date:** 2026-10-15
- **Reviewer:** REVIEW-CODE
- **Mode:** Automated continuation: the user became unavailable during intake. Scope and focus come from the request; remaining intake defaults are recorded here.
- **Scope Type:** Entire Repository
- **Review Focus:** Code correctness and usability, with suggestions for a more fluid and intuitive experience.
- **Scope Boundary:** All tracked files at f6fa6ab; no existing worktree changes. The tracked executable is treated as a distribution artifact, not source.
- **Manifest:** None found.

## Associated ADO Work Items

None, confirmed by the user.

## Associated Documents

| Document | Location | Summary |
|----------|----------|---------|
| README | README.md | Supported commands, user workflows, prerequisites, and key bindings. |
| Architecture | ARCHITECTURE.md | Data access, AppleScript sending, watcher lifecycle, and terminal UI behavior. |

The user confirmed that there are no additional documents.

## PR Discussion Context

None: not a pull request review.

## Active Extensions

None (core review only). No repository or personal review extensions were found. Reviewer alias: dawalton.

## Files In Scope

- .gitignore
- ARCHITECTURE.md
- LICENSE
- README.md
- cmd/imessage/main.go
- go.mod
- go.sum
- imessage (distribution artifact)
- internal/cli/cli.go
- internal/database/contacts.go
- internal/database/database.go
- internal/sender/sender.go
- internal/tui/imagerender.go
- internal/tui/tui.go
- internal/watcher/watcher.go

---

## Appendix: Conventions & Context Notes

### Codebase Conventions

- **Language / Toolchain:** Go; go.mod declares Go 1.24.0. Standard commands are `go build ./cmd/imessage`, `go test ./...`, and `go vet ./...`. SQLite uses CGo.
- **Naming / Layout:** Small internal packages, exported Go types and functions with doc comments, and unexported command handlers. CLI commands are package-level Cobra objects.
- **Idioms / Patterns:** A shared SQL pool initialized with sync.Once; contact maps protected by an RWMutex; watcher callbacks; atomic guards for sends and refreshes; tview.QueueUpdateDraw for background UI updates. Sender uses bounded osascript subprocesses with cascading fallback strategies.
- **Testing:** No tracked test files or test configuration. Review reproductions must use synthetic data rather than personal messages or contacts.
- **Documentation:** README describes commands and key bindings; ARCHITECTURE.md describes component responsibilities and data flow. No coding-standard or linter configuration is tracked.
- **Observability:** CLI prints user-facing errors; watcher has error callbacks; TUI debug mode uses a standard Go logger.
- **Dependencies:** Cobra/pflag for commands; go-sqlite3 for data access; tview/tcell for terminal UI; x/image for image codecs. go.sum pins module checksums.
- **Trust Boundaries:** CLI arguments and composer input; message/contact text read from local SQLite; attachment files; AppleScript strings passed to Messages. Access depends on macOS permissions and the signed-in Messages account.
- **Architecture Seams:** main manages CLI execution and DB shutdown; CLI calls database, sender, and TUI; watcher wraps database records for live callbacks; TUI owns selection, message display, input, and preview state.

### Intent & Requirements

- **Required behavior:** List recent conversations; read recent messages by index or identifier; send messages with optional confirmation; provide interactive chat; search history; show status; provide a live keyboard-driven TUI.
- **Grounding:** README.md usage examples and controls, plus ARCHITECTURE.md package descriptions. No external acceptance criteria or work items were supplied.
- **Non-goals:** Native use on non-macOS platforms is not promised. This review does not implement fixes.
- **Invariants:** Read-only access to chat.db; sends must target the intended recipient/conversation; background work must not block the UI indefinitely; displayed message text must preserve user content; selection and composing must remain consistent as live data changes.
- **Scope:** Whole repository, not a diff. No existing PR discussion.

### Extension Focus

No active extensions - core review only.

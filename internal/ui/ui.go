package ui

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/log"
	"github.com/muesli/termenv"
)

// wisdomQuotes are Harry Potter-inspired quotes for CARD startup.
// [engineer] is replaced with the user's name.
var wisdomQuotes = []string{
	"It is the unknown we fear when we look upon deadlines and darkness, nothing more.",
	"Of course it is happening inside your head, [engineer], but why on earth should that mean that it is not real?",
	"Understanding is the first step to acceptance, and only with acceptance can there be recovery.",
	"It is our choices, [engineer], that show what we truly are, far more than our abilities.",
	"To the well-organized mind, scope creep is but the next great adventure.",
}

// RandomWisdom returns a random wisdom quote with [engineer] replaced by the user's name.
func RandomWisdom() string {
	quote := wisdomQuotes[rand.Intn(len(wisdomQuotes))]
	name := getUserName()
	return strings.ReplaceAll(quote, "[engineer]", name)
}

// WisdomBanner renders the CARD logo alongside an arcane wizard face, with a wisdom quote below.
// This is the polished CARD startup display for `card ask`.
func WisdomBanner() {
	// Card styles (matching Logo())
	cardBorder := lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	cardCorner := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	cardSuit := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))

	// Wizard styles
	hatStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	magicStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	beardStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("248"))

	// Build the card (same as Logo)
	cardTop := cardBorder.Render("╭─────────╮")
	cardL1 := cardBorder.Render("│") + cardCorner.Render("C") + "       " + cardCorner.Render("A") + cardBorder.Render("│")
	cardL2 := cardBorder.Render("│") + "    " + cardSuit.Render("◆") + "    " + cardBorder.Render("│")
	cardL3 := cardBorder.Render("│") + cardCorner.Render("R") + "       " + cardCorner.Render("D") + cardBorder.Render("│")
	cardBot := cardBorder.Render("╰─────────╯")

	// Build the wizard (5 lines to match card) - casting pose with wand raised
	wizL1 := "   " + magicStyle.Render("*") + "  " + magicStyle.Render("~*")
	wizL2 := "  " + hatStyle.Render("/") + magicStyle.Render("^") + hatStyle.Render("\\") + " " + hatStyle.Render("|")
	wizL3 := " " + hatStyle.Render("(") + magicStyle.Render("o.o") + hatStyle.Render(")") + beardStyle.Render("/")
	wizL4 := " " + beardStyle.Render("/") + hatStyle.Render("|") + beardStyle.Render("::") + hatStyle.Render("|")
	wizL5 := " " + hatStyle.Render("/    \\")



	// Get a random quote
	quote := RandomWisdom()
	quoteStyle := lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("248"))
	styledQuote := quoteStyle.Render("\"" + quote + "\"")

	// Render side by side
	spacing := "   "
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, cardTop+spacing+wizL1)
	fmt.Fprintln(os.Stderr, cardL1+spacing+wizL2)
	fmt.Fprintln(os.Stderr, cardL2+spacing+wizL3)
	fmt.Fprintln(os.Stderr, cardL3+spacing+wizL4)
	fmt.Fprintln(os.Stderr, cardBot+spacing+wizL5)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  "+styledQuote)
	fmt.Fprintln(os.Stderr)
}

// getUserName attempts to get the user's name from git config, falling back to sensible defaults.
func getUserName() string {
	// Try git config user.name first
	if name := gitConfigValue("user.name"); name != "" {
		// Use first name only for a friendlier feel
		parts := strings.Fields(name)
		if len(parts) > 0 {
			return parts[0]
		}
		return name
	}

	// Fall back to git config user.email (extract name part before @)
	if email := gitConfigValue("user.email"); email != "" {
		if idx := strings.Index(email, "@"); idx > 0 {
			return email[:idx]
		}
		return email
	}

	return "engineer"
}

// gitConfigValue runs git config --get and returns the value.
func gitConfigValue(key string) string {
	cmd := exec.Command("git", "config", "--get", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Logger is the package-level structured logger.
var Logger *log.Logger

// Styles — initialized in Init().
var (
	headerStyle    lipgloss.Style
	phaseBoxStyle  lipgloss.Style
	successStyle   lipgloss.Style
	warningStyle   lipgloss.Style
	errorStyle     lipgloss.Style
	dimStyle       lipgloss.Style
	boldStyle      lipgloss.Style
	promptStyle    lipgloss.Style
	phaseNameStyle lipgloss.Style
)

// Init sets up color detection, lipgloss styles, and the structured logger.
// Call this once at CLI startup.
func Init(noColorFlag bool) {
	noColor := noColorFlag || os.Getenv("NO_COLOR") != ""

	// Reset terminal to sane state. Some runtimes (like Codex) can leave the terminal
	// in raw mode where \n doesn't include carriage return, causing display corruption.
	// This ensures CARD starts with a clean terminal regardless of previous state.
	SanitizeTerminal()

	// Pre-set dark background to prevent termenv OSC query that leaks ^[[I focus events
	lipgloss.SetHasDarkBackground(true)

	if noColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	phaseBoxStyle = lipgloss.NewStyle().
		Bold(true).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		PaddingLeft(1).
		PaddingRight(1)
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	warningStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	errorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	dimStyle = lipgloss.NewStyle().Faint(true)
	boldStyle = lipgloss.NewStyle().Bold(true)
	promptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	phaseNameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))

	Logger = log.NewWithOptions(os.Stderr, log.Options{
		ReportTimestamp: false,
	})
	if noColor {
		Logger.SetStyles(log.DefaultStyles())
	}
}

// SanitizeTerminal resets the terminal to a sane state.
// This fixes display corruption when the terminal was left in raw mode
// (where \n doesn't reset cursor to column 0) by a previous process.
func SanitizeTerminal() {
	// Use stty sane to reset terminal to normal cooked mode
	cmd := exec.Command("stty", "sane")
	cmd.Stdin = os.Stdin
	_ = cmd.Run()

	// Also print reset escape sequence and carriage return to be safe
	fmt.Fprint(os.Stderr, "\033[0m\r")
}

func Bold(s string) string   { return boldStyle.Render(s) }
func Dim(s string) string    { return dimStyle.Render(s) }
func Red(s string) string    { return errorStyle.Render(s) }
func Green(s string) string  { return successStyle.Render(s) }
func Yellow(s string) string { return warningStyle.Render(s) }

// Logo renders the CARD ASCII art logo to stderr.
// Displays a playing card with C.A.R.D arranged in corners.
func Logo() {
	// Color styles for the logo
	cardBorder := lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	cardCorner := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	cardSuit := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))

	// Build the card lines
	top := cardBorder.Render("╭─────────╮")
	line1 := cardBorder.Render("│") + cardCorner.Render("C") + "       " + cardCorner.Render("A") + cardBorder.Render("│")
	line2 := cardBorder.Render("│") + "    " + cardSuit.Render("◆") + "    " + cardBorder.Render("│")
	line3 := cardBorder.Render("│") + cardCorner.Render("R") + "       " + cardCorner.Render("D") + cardBorder.Render("│")
	bottom := cardBorder.Render("╰─────────╯")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, top)
	fmt.Fprintln(os.Stderr, line1)
	fmt.Fprintln(os.Stderr, line2)
	fmt.Fprintln(os.Stderr, line3)
	fmt.Fprintln(os.Stderr, bottom)
}

// LogoWithTagline renders the CARD logo with a tagline underneath.
func LogoWithTagline(tagline string) {
	Logo()
	if tagline != "" {
		fmt.Fprintln(os.Stderr, dimStyle.Render("  "+tagline))
	}
	fmt.Fprintln(os.Stderr)
}

// Status prints a styled status message.
func Status(msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", phaseNameStyle.Render("▸"), msg)
}

// PhaseHeader renders a prominent CARD-branded phase banner with progress indicator.
func PhaseHeader(phase string, index, total int, repoName, repoID string) {
	// Reset cursor to column 0 before rendering the banner.
	// This prevents display issues when terminal was left in raw mode
	// (e.g., after bubbletea prompts) where \n doesn't implicitly include \r.
	fmt.Fprint(os.Stderr, "\r")

	cardBrand := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Render("C · A · R · D")

	phaseLine := fmt.Sprintf("─── %s (%d/%d) ───", strings.ToUpper(phase), index, total)
	phaseStyled := phaseNameStyle.Render(phaseLine)

	repoLine := dimStyle.Render(fmt.Sprintf("repo: %s (%s)", repoName, repoID))

	content := fmt.Sprintf("%s\n%s\n%s", cardBrand, phaseStyled, repoLine)

	box := lipgloss.NewStyle().
		Bold(true).
		BorderStyle(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("63")).
		PaddingLeft(2).
		PaddingRight(2).
		PaddingTop(0).
		PaddingBottom(0).
		Render(content)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, box)
	fmt.Fprintln(os.Stderr)
}

// PhaseLaunch prints a styled "launching runtime" message.
// If interactive, shows a green prompt telling the user to type "Go".
func PhaseLaunch(phase, runtimeName string, interactive bool) {
	display := strings.ToUpper(runtimeName)
	if strings.TrimSpace(display) == "" {
		display = "RUNTIME"
	}
	fmt.Fprintf(os.Stderr, "\n%s Launching %s for %s phase\n",
		headerStyle.Render("──"), boldStyle.Render(display), boldStyle.Render(strings.ToUpper(phase)))
	if interactive {
		fmt.Fprintf(os.Stderr, "%s\n\n",
			successStyle.Render("    ➤ Type \"Go\" below to begin"))
	} else {
		fmt.Fprintln(os.Stderr)
	}
}

// PhaseComplete prints a styled phase completion message.
func PhaseComplete(phase string) {
	fmt.Fprintf(os.Stderr, "%s %s phase complete\n",
		successStyle.Render("✓"), strings.ToUpper(phase))
}

// SessionHeader prints a styled session start banner.
func SessionHeader(id, description string) {
	// Reset cursor to column 0 (see PhaseHeader comment)
	fmt.Fprint(os.Stderr, "\r")

	box := lipgloss.NewStyle().
		Bold(true).
		BorderStyle(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("12")).
		PaddingLeft(1).
		PaddingRight(1).
		Render(fmt.Sprintf("SESSION: %s\n%s", id, dimStyle.Render(description)))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, box)
	fmt.Fprintln(os.Stderr)
}

// SessionComplete prints a styled session completion message.
func SessionComplete(id string) {
	fmt.Fprintf(os.Stderr, "\n%s\n",
		successStyle.Render(fmt.Sprintf("✓ Session %s completed successfully", id)))
}

// Warning prints a styled warning message.
func Warning(msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", warningStyle.Render("⚠"), msg)
}

// Error prints a styled error message.
func Error(msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", errorStyle.Render("✗"), msg)
}

// Info prints a styled informational message.
func Info(msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", phaseNameStyle.Render("▸"), msg)
}

// Table prints a formatted table with headers and rows.
func Table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, boldStyle.Render(strings.Join(headers, "\t")))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
}

// Success prints a green check with a message.
func Success(msg string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", successStyle.Render("✓"), msg)
}

// Detail prints an indented key-value detail line.
func Detail(key, value string) {
	label := dimStyle.Render(fmt.Sprintf("  %s", key))
	fmt.Fprintf(os.Stderr, "%s %s\n", label, value)
}

// KeyValue prints a bold key with a value, for structured output blocks.
func KeyValue(key, value string) {
	fmt.Fprintf(os.Stderr, "  %s  %s\n", boldStyle.Render(key), value)
}

// SectionHeader prints a styled section divider with a label.
func SectionHeader(label string) {
	line := headerStyle.Render(fmt.Sprintf("── %s ──", label))
	fmt.Fprintf(os.Stderr, "\n%s\n\n", line)
}

// EmptyState prints a styled message for empty results.
func EmptyState(msg string) {
	fmt.Fprintf(os.Stderr, "  %s\n", dimStyle.Render(msg))
}

// CommandBanner renders a small CARD-branded banner for a command.
func CommandBanner(command string, subtitle string) {
	// Reset cursor to column 0 (see PhaseHeader comment)
	fmt.Fprint(os.Stderr, "\r")

	brand := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Render("C · A · R · D")

	cmdLine := phaseNameStyle.Render(fmt.Sprintf("─── %s ───", strings.ToUpper(command)))

	content := fmt.Sprintf("%s\n%s", brand, cmdLine)
	if subtitle != "" {
		content += "\n" + dimStyle.Render(subtitle)
	}

	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		PaddingLeft(1).
		PaddingRight(1).
		Render(content)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, box)
	fmt.Fprintln(os.Stderr)
}

// =============================================================================
// Bubbletea-based interactive prompts
// =============================================================================

// confirmModel is a bubbletea model for y/n confirmation.
type confirmModel struct {
	prompt   string
	cursor   int // 0 = yes, 1 = no
	decided  bool
	accepted bool
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			m.accepted = true
			m.decided = true
			return m, tea.Quit
		case "n", "N":
			m.accepted = false
			m.decided = true
			return m, tea.Quit
		case "left", "h":
			m.cursor = 0
		case "right", "l":
			m.cursor = 1
		case "enter", " ":
			m.accepted = m.cursor == 0
			m.decided = true
			return m, tea.Quit
		case "ctrl+c", "esc":
			m.accepted = false
			m.decided = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmModel) View() string {
	yes := "  Yes  "
	no := "  No  "

	if m.cursor == 0 {
		yes = successStyle.Render("▸ Yes ")
		no = dimStyle.Render("  No  ")
	} else {
		yes = dimStyle.Render("  Yes ")
		no = errorStyle.Render("▸ No  ")
	}

	return fmt.Sprintf("%s\n\n  %s  %s\n\n%s",
		promptStyle.Render(m.prompt),
		yes, no,
		dimStyle.Render("  ←/→ to select • enter to confirm • y/n for quick select"))
}

// Confirm prompts the user with a yes/no question and returns the response.
func Confirm(prompt string) (bool, error) {
	m := confirmModel{prompt: prompt, cursor: 0}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		return false, err
	}
	fmt.Fprintln(os.Stderr) // newline after prompt
	return result.(confirmModel).accepted, nil
}

// confirmOrSelectModel is a bubbletea model for y/n/s selection.
type confirmOrSelectModel struct {
	prompt  string
	cursor  int // 0 = yes, 1 = no, 2 = select
	decided bool
	result  string
}

func (m confirmOrSelectModel) Init() tea.Cmd { return nil }

func (m confirmOrSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			m.result = "yes"
			m.decided = true
			return m, tea.Quit
		case "n", "N":
			m.result = "no"
			m.decided = true
			return m, tea.Quit
		case "s", "S":
			m.result = "select"
			m.decided = true
			return m, tea.Quit
		case "left", "h":
			if m.cursor > 0 {
				m.cursor--
			}
		case "right", "l":
			if m.cursor < 2 {
				m.cursor++
			}
		case "enter", " ":
			switch m.cursor {
			case 0:
				m.result = "yes"
			case 1:
				m.result = "no"
			case 2:
				m.result = "select"
			}
			m.decided = true
			return m, tea.Quit
		case "ctrl+c", "esc":
			m.result = "no"
			m.decided = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmOrSelectModel) View() string {
	options := []string{"  Yes  ", "  No  ", "  Select  "}
	styles := []lipgloss.Style{successStyle, errorStyle, promptStyle}

	for i := range options {
		if i == m.cursor {
			options[i] = styles[i].Render(fmt.Sprintf("▸%s", strings.TrimPrefix(options[i], "  ")))
		} else {
			options[i] = dimStyle.Render(options[i])
		}
	}

	return fmt.Sprintf("%s\n\n  %s  %s  %s\n\n%s",
		promptStyle.Render(m.prompt),
		options[0], options[1], options[2],
		dimStyle.Render("  ←/→ to select • enter to confirm • y/n/s for quick select"))
}

// ConfirmOrSelect prompts the user with yes/no/select options.
// Returns "yes", "no", or "select".
func ConfirmOrSelect(prompt string) (string, error) {
	m := confirmOrSelectModel{prompt: prompt, cursor: 0}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		return "no", err
	}
	fmt.Fprintln(os.Stderr) // newline after prompt
	return result.(confirmOrSelectModel).result, nil
}

// CommitItem represents a commit for selection.
type CommitItem struct {
	SHA      string
	Message  string
	Selected bool
}

// selectCommitsModel is a bubbletea model for multi-select commit list.
type selectCommitsModel struct {
	repoName  string
	commits   []CommitItem
	cursor    int
	confirmed bool
}

func (m selectCommitsModel) Init() tea.Cmd { return nil }

func (m selectCommitsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.commits)-1 {
				m.cursor++
			}
		case " ", "x":
			m.commits[m.cursor].Selected = !m.commits[m.cursor].Selected
		case "a":
			for i := range m.commits {
				m.commits[i].Selected = true
			}
		case "n":
			for i := range m.commits {
				m.commits[i].Selected = false
			}
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "ctrl+c", "esc":
			// Cancel - deselect all
			for i := range m.commits {
				m.commits[i].Selected = false
			}
			m.confirmed = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectCommitsModel) View() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("\n  %s\n", boldStyle.Render(m.repoName)))
	b.WriteString(fmt.Sprintf("  %s\n\n", dimStyle.Render("↑/↓ navigate • space toggle • a all • n none • enter confirm")))

	for i, c := range m.commits {
		cursor := "  "
		if i == m.cursor {
			cursor = promptStyle.Render("▸ ")
		}

		checkbox := "[ ]"
		if c.Selected {
			checkbox = successStyle.Render("[✓]")
		}

		sha := c.SHA
		if len(sha) > 7 {
			sha = sha[:7]
		}

		msg := c.Message
		if len(msg) > 50 {
			msg = msg[:50] + ".."
		}

		b.WriteString(fmt.Sprintf("%s%s  %s  %s\n", cursor, checkbox, dimStyle.Render(sha), msg))
	}

	return b.String()
}

// SelectCommits displays an interactive commit selection interface.
// Returns the indices of selected commits.
func SelectCommits(repoName string, commits []CommitItem) ([]int, error) {
	if len(commits) == 0 {
		return nil, nil
	}

	// Start with all selected
	for i := range commits {
		commits[i].Selected = true
	}

	m := selectCommitsModel{repoName: repoName, commits: commits, cursor: 0}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		return nil, err
	}

	fmt.Fprintln(os.Stderr) // newline after prompt

	// Collect selected indices
	final := result.(selectCommitsModel)
	var selected []int
	for i, c := range final.commits {
		if c.Selected {
			selected = append(selected, i)
		}
	}
	return selected, nil
}

// verifyDecisionModel is a bubbletea model for verification decision.
type verifyDecisionModel struct {
	cursor  int // 0 = accept, 1 = reexecute, 2 = pause
	decided bool
	result  string
}

func (m verifyDecisionModel) Init() tea.Cmd { return nil }

func (m verifyDecisionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "a", "A":
			m.result = "accept"
			m.decided = true
			return m, tea.Quit
		case "r", "R":
			m.result = "reexecute"
			m.decided = true
			return m, tea.Quit
		case "p", "P":
			m.result = "pause"
			m.decided = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < 2 {
				m.cursor++
			}
		case "enter", " ":
			switch m.cursor {
			case 0:
				m.result = "accept"
			case 1:
				m.result = "reexecute"
			case 2:
				m.result = "pause"
			}
			m.decided = true
			return m, tea.Quit
		case "ctrl+c", "esc":
			m.result = "pause"
			m.decided = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m verifyDecisionModel) View() string {
	options := []struct {
		key   string
		label string
		desc  string
	}{
		{"a", "Accept", "Proceed to simplification"},
		{"r", "Re-execute", "Run execute + verify again"},
		{"p", "Pause", "Pause session for later"},
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(phaseBoxStyle.Render("  Verification Complete  "))
	b.WriteString("\n\n")

	for i, opt := range options {
		cursor := "  "
		if i == m.cursor {
			cursor = promptStyle.Render("▸ ")
		}

		key := dimStyle.Render(fmt.Sprintf("[%s]", opt.key))
		label := opt.label
		if i == m.cursor {
			label = boldStyle.Render(label)
		}

		b.WriteString(fmt.Sprintf("%s%s %s  %s\n", cursor, key, label, dimStyle.Render(opt.desc)))
	}

	b.WriteString(fmt.Sprintf("\n%s", dimStyle.Render("  ↑/↓ navigate • enter confirm • a/r/p quick select")))

	return b.String()
}

// VerifyDecision renders a styled verify decision prompt.
func VerifyDecision() (string, error) {
	m := verifyDecisionModel{cursor: 0}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		return "accept", err
	}
	fmt.Fprintln(os.Stderr) // newline after prompt
	return result.(verifyDecisionModel).result, nil
}

// ApprovalPrompt renders a styled phase approval prompt.
func ApprovalPrompt(completedPhase, nextPhase string) (bool, error) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s %s phase complete\n",
		successStyle.Render("──"), strings.ToUpper(completedPhase))

	return Confirm(fmt.Sprintf("Proceed to %s?", nextPhase))
}

// Spinner displays an animated spinner with a message on stderr.
// Call Stop() to clear it. Stop() is safe to call multiple times.
type Spinner struct {
	msg      string
	stop     chan struct{}
	done     sync.WaitGroup
	stopOnce sync.Once
}

// NewSpinner starts a spinner with the given message.
func NewSpinner(msg string) *Spinner {
	s := &Spinner{
		msg:  msg,
		stop: make(chan struct{}),
	}
	s.done.Add(1)
	go s.run()
	return s
}

func (s *Spinner) run() {
	defer s.done.Done()
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	// Render first frame immediately so spinner is visible even if stopped quickly
	frame := phaseNameStyle.Render(frames[0])
	fmt.Fprintf(os.Stderr, "\r%s %s", frame, dimStyle.Render(s.msg))
	i++

	for {
		select {
		case <-s.stop:
			fmt.Fprintf(os.Stderr, "\r\033[K")
			return
		case <-ticker.C:
			frame := phaseNameStyle.Render(frames[i%len(frames)])
			fmt.Fprintf(os.Stderr, "\r%s %s", frame, dimStyle.Render(s.msg))
			i++
		}
	}
}

// Stop halts the spinner and clears its line.
// Safe to call multiple times.
func (s *Spinner) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
	})
	s.done.Wait()
}

// =============================================================================
// Pensieve Animation — memory extraction intro for `card ask`
// =============================================================================

// Animation phases for the Pensieve effect
type pensievePhase int

const (
	phaseGathering pensievePhase = iota // Dots materialize below wizard
	phaseChanneling                     // Magic rises through wizard, wand glows
	phaseTransmitting                   // Filament arcs toward card
	phaseSealing                        // Card absorbs and solidifies
	phaseComplete                       // Static final state
)

// pensieveModel is a bubbletea model for the Pensieve intro animation.
type pensieveModel struct {
	phase       pensievePhase
	frame       int
	quote       string
	totalFrames int
}

// tickMsg triggers the next animation frame.
type tickMsg struct{}

func pensieveTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func newPensieveModel() pensieveModel {
	return pensieveModel{
		phase:       phaseGathering,
		frame:       0,
		quote:       RandomWisdom(),
		totalFrames: 12, // ~600ms total at 50ms/frame
	}
}

func (m pensieveModel) Init() tea.Cmd {
	return pensieveTick()
}

func (m pensieveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tickMsg:
		m.frame++

		// Phase transitions based on frame count
		switch {
		case m.frame < 3:
			m.phase = phaseGathering
		case m.frame < 5:
			m.phase = phaseChanneling
		case m.frame < 9:
			m.phase = phaseTransmitting
		case m.frame < 11:
			m.phase = phaseSealing
		default:
			m.phase = phaseComplete
			return m, tea.Quit
		}

		return m, pensieveTick()

	case tea.KeyMsg:
		// Allow skipping animation
		m.phase = phaseComplete
		return m, tea.Quit
	}

	return m, nil
}

func (m pensieveModel) View() string {
	var b strings.Builder

	// Styles
	cardBorder := lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	cardBorderDim := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	cardCorner := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	cardCornerDim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	cardSuit := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	cardSuitDim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	hatStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	magicStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	beardStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	filamentStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	filamentDim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	filamentFaint := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	quoteStyle := lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("248"))

	// Determine card brightness based on phase
	useDimCard := m.phase < phaseSealing
	cBorder, cCorner, cSuit := cardBorder, cardCorner, cardSuit
	if useDimCard {
		cBorder, cCorner, cSuit = cardBorderDim, cardCornerDim, cardSuitDim
	}

	// Build card lines
	cardTop := cBorder.Render("╭─────────╮")
	cardL1 := cBorder.Render("│") + cCorner.Render("C") + "       " + cCorner.Render("A") + cBorder.Render("│")
	cardL2 := cBorder.Render("│") + "    " + cSuit.Render("◆") + "    " + cBorder.Render("│")
	cardL3 := cBorder.Render("│") + cCorner.Render("R") + "       " + cCorner.Render("D") + cBorder.Render("│")
	cardBot := cBorder.Render("╰─────────╯")

	// Build wizard lines based on phase
	// All lines are padded to 8 characters for consistent alignment
	var wizL1, wizL2, wizL3, wizL4, wizL5 string

	// Common wizard body (lines 2-5 stay consistent, only line 1 changes)
	wizL2 = "  " + hatStyle.Render("/") + magicStyle.Render("^") + hatStyle.Render("\\") + " " + hatStyle.Render("|") + " "  // 8 chars
	wizL3 = " " + hatStyle.Render("(") + magicStyle.Render("o.o") + hatStyle.Render(")") + beardStyle.Render("/") + " "      // 8 chars
	wizL4 = " " + beardStyle.Render("/") + hatStyle.Render("|") + beardStyle.Render("::") + hatStyle.Render("|") + "  "      // 8 chars
	wizL5 = " " + hatStyle.Render("/    \\") + " "                                                                            // 8 chars

	switch m.phase {
	case phaseGathering:
		// Wizard dormant, no sparkles yet
		wizL1 = "        " // 8 spaces

	case phaseChanneling:
		// Wand begins to glow (single sparkle)
		wizL1 = "   " + magicStyle.Render("*") + "    " // 8 chars: 3+1+4=8

	case phaseTransmitting:
		// Full sparkle, filament traveling
		wizL1 = "   " + magicStyle.Render("*") + "  " + magicStyle.Render("~*") // 8 chars: 3+1+2+2=8

	case phaseSealing:
		// Sparkle dims briefly during absorption
		wizL1 = "   " + filamentDim.Render("·") + "  " + filamentDim.Render("~·") // 8 chars: 3+1+2+2=8

	case phaseComplete:
		// Spell complete — wand retains its glow
		wizL1 = "   " + magicStyle.Render("*") + "  " + magicStyle.Render("~*") // 8 chars: 3+1+2+2=8
	}

	// Build the filament bridge between wizard and card
	// The gap is where the filament travels (wizard on left, card on right)
	var gap [5]string
	gapWidth := 6 // characters between wizard and card

	switch m.phase {
	case phaseGathering:
		// Empty gap, maybe faint dots materializing below
		for i := range gap {
			gap[i] = strings.Repeat(" ", gapWidth)
		}

	case phaseChanneling:
		// Energy gathering at wand tip
		gap[0] = strings.Repeat(" ", gapWidth)
		gap[1] = strings.Repeat(" ", gapWidth)
		gap[2] = strings.Repeat(" ", gapWidth)
		gap[3] = strings.Repeat(" ", gapWidth)
		gap[4] = strings.Repeat(" ", gapWidth)

	case phaseTransmitting:
		// Filament traveling across the gap
		// Frame 5-8: filament progresses left to right
		progress := m.frame - 5 // 0, 1, 2, 3
		filament := []string{"•", "∙", "·", " "}

		for i := range gap {
			gap[i] = strings.Repeat(" ", gapWidth)
		}

		// Filament on line 2 (middle-ish), traveling toward card
		if progress >= 0 && progress < 4 {
			line := ""
			for pos := 0; pos < gapWidth; pos++ {
				headPos := progress + 1 // where the bright head is
				if pos == headPos {
					line += filamentStyle.Render(filament[0])
				} else if pos == headPos-1 && headPos > 0 {
					line += filamentDim.Render(filament[1])
				} else if pos == headPos-2 && headPos > 1 {
					line += filamentFaint.Render(filament[2])
				} else {
					line += " "
				}
			}
			gap[2] = line
		}

	case phaseSealing:
		// Filament absorbed, brief pulse
		for i := range gap {
			gap[i] = strings.Repeat(" ", gapWidth)
		}
		// Show fading trail
		if m.frame == 9 {
			gap[2] = strings.Repeat(" ", 3) + filamentFaint.Render("··") + " "
		}

	case phaseComplete:
		for i := range gap {
			gap[i] = strings.Repeat(" ", gapWidth)
		}
	}

	// Compose the scene: wizard (left) + gap + card (right)
	b.WriteString("\n")
	b.WriteString(" " + wizL1 + gap[0] + cardTop + "\n")
	b.WriteString(" " + wizL2 + gap[1] + cardL1 + "\n")
	b.WriteString(" " + wizL3 + gap[2] + cardL2 + "\n")
	b.WriteString(" " + wizL4 + gap[3] + cardL3 + "\n")
	b.WriteString(" " + wizL5 + gap[4] + cardBot + "\n")
	b.WriteString("\n")

	// Quote fades in at the end
	if m.phase == phaseComplete || m.phase == phaseSealing {
		b.WriteString("  " + quoteStyle.Render("\""+m.quote+"\"") + "\n")
	}
	b.WriteString("\n")

	return b.String()
}

// AnimatedWisdomBanner runs the Pensieve-style intro animation.
// Memory is extracted from the workspace and sealed into the CARD artifact.
// Falls back to static WisdomBanner if animation fails.
func AnimatedWisdomBanner() {
	m := newPensieveModel()
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))

	_, err := p.Run()
	if err != nil {
		// Fall back to static banner
		WisdomBanner()
	}
	// bubbletea leaves the final View() on screen, no need to reprint
}

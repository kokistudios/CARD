package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/kokistudios/card/internal/bundle"
	"github.com/kokistudios/card/internal/capsule"
	"github.com/kokistudios/card/internal/change"
	"github.com/kokistudios/card/internal/claude"
	cardmcp "github.com/kokistudios/card/internal/mcp"
	"github.com/kokistudios/card/internal/phase"
	"github.com/kokistudios/card/internal/recall"
	"github.com/kokistudios/card/internal/repo"
	"github.com/kokistudios/card/internal/session"
	"github.com/kokistudios/card/internal/store"
	"github.com/kokistudios/card/internal/ui"
)

// Set via ldflags at build time
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func buildVersion() string {
	if commit == "none" {
		return version
	}
	return fmt.Sprintf("%s (%s, %s)", version, commit, date)
}

func main() {
	var noColor bool

	rootCmd := &cobra.Command{
		Use:   "card",
		Short: "CARD — Context Artifact Relay Development",
		Long:  "A local CLI tool that captures, structures, and reuses engineering intent across code changes.",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			ui.Init(noColor)
			// Ensure MCP is configured on every invocation (fast, silent unless changes made)
			ensureMCPConfigured(false)
		},
	}

	rootCmd.Version = buildVersion()
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")

	// Command groups
	rootCmd.AddGroup(
		&cobra.Group{ID: "core", Title: "Core Commands:"},
		&cobra.Group{ID: "session", Title: "Session Commands:"},
		&cobra.Group{ID: "memory", Title: "Memory Commands:"},
		&cobra.Group{ID: "config", Title: "Configuration:"},
	)

	initC := initCmd()
	initC.GroupID = "core"
	doctorC := doctorCmd()
	doctorC.GroupID = "core"

	repoC := repoCmd()
	repoC.GroupID = "core"

	sessionC := sessionCmd()
	sessionC.GroupID = "session"

	capsuleC := capsuleCmd()
	capsuleC.GroupID = "memory"
	recallC := recallCmd()
	recallC.GroupID = "memory"

	configC := configCmd()
	configC.GroupID = "config"

	rootCmd.AddCommand(initC)
	rootCmd.AddCommand(repoC)
	rootCmd.AddCommand(sessionC)
	rootCmd.AddCommand(capsuleC)
	rootCmd.AddCommand(recallC)
	rootCmd.AddCommand(doctorC)
	rootCmd.AddCommand(configC)
	comcapC := comcapCmd()
	comcapC.GroupID = "session"
	rootCmd.AddCommand(comcapC)
	rootCmd.AddCommand(cleanCmd())
	rootCmd.AddCommand(completionCmd())

	// Export/Import commands
	exportC := exportCmd()
	exportC.GroupID = "memory"
	rootCmd.AddCommand(exportC)
	importC := importCmd()
	importC.GroupID = "memory"
	rootCmd.AddCommand(importC)
	preflightC := preflightCmd()
	preflightC.GroupID = "memory"
	rootCmd.AddCommand(preflightC)

	// MCP and Ask commands
	askC := askCmd()
	askC.GroupID = "core"
	rootCmd.AddCommand(askC)
	rootCmd.AddCommand(mcpServeCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Initialize CARD_HOME directory structure",
		Long:    "Create the CARD_HOME directory (~/.card by default) with repos/, sessions/, and config.yaml. Run this once before using any other CARD commands.",
		Example: "  card init\n  card init --force",
		RunE: func(cmd *cobra.Command, args []string) error {
			home := store.Home()
			if err := store.Init(home, force); err != nil {
				return err
			}
			ui.Success("CARD initialized")
			ui.Detail("Home:", home)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Reinitialize even if CARD_HOME already exists")
	return cmd
}

func repoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Manage registered repositories",
		Long:  "Register, list, and remove repositories that CARD tracks. Repos are identified by a stable hash of their git remote URL.",
	}

	cmd.AddCommand(repoAddCmd())
	cmd.AddCommand(repoListCmd())
	cmd.AddCommand(repoRemoveCmd())
	return cmd
}

func repoAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "add <path>",
		Short:   "Register a repository with CARD",
		Example: "  card repo add /path/to/my/repo\n  card repo add .",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Load(store.Home())
			if err != nil {
				return fmt.Errorf("CARD not initialized — run 'card init' first: %w", err)
			}
			r, err := repo.Register(s, args[0])
			if err != nil {
				return err
			}
			ui.Success("Repo registered")
			ui.KeyValue("ID:    ", r.ID)
			ui.KeyValue("Name:  ", r.Name)
			ui.KeyValue("Remote:", r.RemoteURL)
			ui.KeyValue("Path:  ", r.LocalPath)
			return nil
		},
	}
}

func repoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all registered repositories",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Load(store.Home())
			if err != nil {
				return fmt.Errorf("CARD not initialized — run 'card init' first: %w", err)
			}
			repos, err := repo.List(s)
			if err != nil {
				return err
			}
			if len(repos) == 0 {
				ui.EmptyState("No repos registered. Use 'card repo add <path>' to register one.")
				return nil
			}
			var rows [][]string
			for _, r := range repos {
				rows = append(rows, []string{r.ID, r.Name, r.LocalPath})
			}
			ui.Table([]string{"ID", "NAME", "PATH"}, rows)
			return nil
		},
	}
}

func repoRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <repo-id>",
		Short: "Deregister a repository",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := store.Load(store.Home())
			if err != nil {
				return fmt.Errorf("CARD not initialized — run 'card init' first: %w", err)
			}
			proceed, err := ui.Confirm(fmt.Sprintf("Remove repo %s?", args[0]))
			if err != nil {
				return err
			}
			if !proceed {
				ui.Info("Cancelled.")
				return nil
			}
			if err := repo.Remove(s, args[0]); err != nil {
				return err
			}
			ui.Success(fmt.Sprintf("Removed repo %s", args[0]))
			return nil
		},
	}
}

func loadStore() (*store.Store, error) {
	s, err := store.Load(store.Home())
	if err != nil {
		return nil, fmt.Errorf("CARD not initialized — run 'card init' first: %w", err)
	}
	return s, nil
}

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage CARD sessions",
		Long:  "Create, list, pause, resume, retry, and complete sessions. A session is a unit of work spanning one or more repos through the CARD phase pipeline.",
	}
	cmd.AddCommand(sessionStartCmd())
	cmd.AddCommand(sessionListCmd())
	cmd.AddCommand(sessionStatusCmd())
	cmd.AddCommand(sessionPauseCmd())
	cmd.AddCommand(sessionResumeCmd())
	cmd.AddCommand(sessionEndCmd())
	cmd.AddCommand(sessionAbandonCmd())
	cmd.AddCommand(sessionRetryCmd())
	return cmd
}

func sessionStartCmd() *cobra.Command {
	var repoIDs []string
	var noRun bool
	var dryRun bool
	var contextFlag string
	cmd := &cobra.Command{
		Use:   "start <description>",
		Short: "Start a new session",
		Long:  "Start a new CARD session for one or more repositories. This launches the phase pipeline (investigate → plan → execute → simplify → record) unless --no-run is specified.",
		Example: `  card session start "Add user authentication" --repo myapi -c "Users need OAuth2 login with Google and GitHub providers"
  card session start "Migrate database" --repo myapi --repo myfrontend --context context.md
  card session start "Debug login" --repo myapi --no-run -c "Login fails with 403 after session timeout"
  card session start "Add caching" --repo myapi --dry-run -c "Redis-based caching for API responses"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			// Resolve repo paths to IDs
			var ids []string
			for _, r := range repoIDs {
				if _, err := repo.Get(s, r); err == nil {
					ids = append(ids, r)
					continue
				}
				return fmt.Errorf("repo not registered: %s (use 'card repo add' first)", r)
			}

			// Dry run: preview what would happen without creating anything
			if dryRun {
				ui.CommandBanner("DRY RUN", args[0])
				ui.KeyValue("Description:", args[0])
				ui.SectionHeader("Repos")
				for _, id := range ids {
					r, _ := repo.Get(s, id)
					if r != nil {
						ui.Detail(r.Name, ui.Dim(r.LocalPath))
					} else {
						ui.Detail(id, "")
					}
				}

				// Show recall context
				for _, repoID := range ids {
					r, _ := repo.Get(s, repoID)
					repoPath := ""
					if r != nil {
						repoPath = r.LocalPath
					}
					result, err := recall.Query(s, recall.RecallQuery{
						RepoID:      repoID,
						RepoPath:    repoPath,
						MaxCapsules: s.Config.Recall.MaxContextBlocks,
					})
					if err == nil && len(result.Capsules) > 0 {
						ui.SectionHeader(fmt.Sprintf("Recall: %s", repoID))
						fmt.Print(recall.FormatTerminal(result, false))
					} else {
						ui.EmptyState(fmt.Sprintf("No prior context found for %s", repoID))
					}
				}

				ui.Info("Investigation prompt would be rendered with the above context.")
				ui.EmptyState("No files created. No Claude Code invocation.")
				return nil
			}

			// Resolve context: if it's a file path, read the file; otherwise use as inline text
			var createOpts []session.CreateOption
			if contextFlag != "" {
				ctxContent := contextFlag
				if data, err := os.ReadFile(contextFlag); err == nil {
					ctxContent = string(data)
				}
				createOpts = append(createOpts, session.WithContext(ctxContent))
			}

			sess, err := session.Create(s, args[0], ids, createOpts...)
			if err != nil {
				return err
			}

			for _, repoID := range ids {
				if _, err := change.Create(s, sess.ID, repoID); err != nil {
					return fmt.Errorf("failed to create change for repo %s: %w", repoID, err)
				}
			}

			ui.Success("Session started")
			ui.KeyValue("ID:    ", ui.Bold(sess.ID))
			ui.KeyValue("Status:", ui.Green(string(sess.Status)))
			ui.KeyValue("Repos: ", ui.Dim(strings.Join(ids, ", ")))

			if noRun {
				return nil
			}

			return phase.RunSession(s, sess)
		},
	}
	cmd.Flags().StringArrayVar(&repoIDs, "repo", nil, "Repo ID to include (can be specified multiple times)")
	cmd.Flags().StringVarP(&contextFlag, "context", "c", "", "Context file path or inline text to provide to the investigation phase")
	cmd.Flags().BoolVar(&noRun, "no-run", false, "Create session without launching phase pipeline")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what would happen without creating anything")
	_ = cmd.MarkFlagRequired("repo")
	_ = cmd.MarkFlagRequired("context")
	return cmd
}

func sessionListCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			var sessions []session.Session
			if all {
				sessions, err = session.List(s)
			} else {
				sessions, err = session.GetActive(s)
			}
			if err != nil {
				return err
			}

			if len(sessions) == 0 {
				ui.EmptyState("No sessions found.")
				return nil
			}

			var rows [][]string
			for _, sess := range sessions {
				desc := sess.Description
				if len(desc) > 28 {
					desc = desc[:28] + ".."
				}
				rows = append(rows, []string{sess.ID, desc, string(sess.Status), strings.Join(sess.Repos, ", ")})
			}
			ui.Table([]string{"ID", "DESCRIPTION", "STATUS", "REPOS"}, rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Show all sessions including completed and abandoned")
	return cmd
}

func sessionStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [session-id]",
		Short: "Show session status",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				// Show all active sessions
				sessions, err := session.GetActive(s)
				if err != nil {
					return err
				}
				if len(sessions) == 0 {
					ui.EmptyState("No active sessions.")
					return nil
				}
				for _, sess := range sessions {
					printSessionDetail(s, &sess)
				}
				return nil
			}

			sess, err := session.Get(s, args[0])
			if err != nil {
				return err
			}
			printSessionDetail(s, sess)
			return nil
		},
	}
}

func printSessionDetail(s *store.Store, sess *session.Session) {
	// Status color
	statusStr := string(sess.Status)
	switch sess.Status {
	case session.StatusCompleted:
		statusStr = ui.Green(statusStr)
	case session.StatusPaused:
		statusStr = ui.Yellow(statusStr)
	case session.StatusAbandoned:
		statusStr = ui.Red(statusStr)
	default:
		statusStr = ui.Bold(statusStr)
	}

	ui.SectionHeader(sess.ID)
	ui.KeyValue("Description:", sess.Description)
	ui.KeyValue("Status:     ", statusStr)
	if sess.Mode == session.ModeQuickfix {
		ui.KeyValue("Mode:       ", ui.Bold("quickfix")+" (Execute → Verify → Record)")
	}
	ui.KeyValue("Created:    ", sess.CreatedAt.Format("2006-01-02 15:04:05"))
	ui.KeyValue("Updated:    ", sess.UpdatedAt.Format("2006-01-02 15:04:05"))
	if sess.PausedAt != nil {
		ui.KeyValue("Paused:     ", sess.PausedAt.Format("2006-01-02 15:04:05"))
	}
	if sess.CompletedAt != nil {
		ui.KeyValue("Completed:  ", sess.CompletedAt.Format("2006-01-02 15:04:05"))
	}

	for _, repoID := range sess.Repos {
		r, _ := repo.Get(s, repoID)
		repoLabel := repoID
		if r != nil {
			repoLabel = fmt.Sprintf("%s %s", r.Name, ui.Dim("("+repoID+")"))
		}
		ui.Detail("repo:", repoLabel)
		c, err := change.Get(s, sess.ID, repoID)
		if err == nil {
			if c.BaseCommit != "" {
				ui.Detail("  base:", shortSHA(c.BaseCommit))
			}
			if c.FinalCommit != "" {
				ui.Detail("  head:", ui.Green(shortSHA(c.FinalCommit)))
			}
			if len(c.Artifacts) > 0 {
				ui.Detail("  artifacts:", fmt.Sprintf("%d", len(c.Artifacts)))
			}
		}
	}
}

func resolveSessionID(s *store.Store, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	active, err := session.GetActive(s)
	if err != nil {
		return "", err
	}
	if len(active) == 0 {
		return "", fmt.Errorf("no active sessions")
	}
	if len(active) > 1 {
		return "", fmt.Errorf("multiple active sessions — specify a session ID")
	}
	return active[0].ID, nil
}

func sessionPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause [session-id]",
		Short: "Pause a session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			id, err := resolveSessionID(s, args)
			if err != nil {
				return err
			}
			if err := session.Pause(s, id); err != nil {
				return err
			}
			ui.Success(fmt.Sprintf("Session %s paused", id))
			return nil
		},
	}
}

func sessionResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume [session-id]",
		Short: "Resume a paused or interrupted session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			// Find resumable sessions: paused or stuck in an active state
			if len(args) == 0 {
				all, err := session.GetActive(s)
				if err != nil {
					return err
				}
				var resumable []session.Session
				for _, sess := range all {
					if sess.Status == session.StatusPaused {
						resumable = append(resumable, sess)
					}
				}
				// Also consider stuck active sessions (not started — that's fresh)
				if len(resumable) == 0 {
					for _, sess := range all {
						if sess.Status != session.StatusStarted && sess.Status != session.StatusPaused {
							resumable = append(resumable, sess)
						}
					}
				}
				if len(resumable) == 0 {
					return fmt.Errorf("no resumable sessions")
				}
				if len(resumable) > 1 {
					return fmt.Errorf("multiple resumable sessions — specify a session ID")
				}
				args = []string{resumable[0].ID}
			}
			if err := session.Resume(s, args[0]); err != nil {
				return err
			}
			// Re-load and re-launch the pipeline from the current phase
			sess, err := session.Get(s, args[0])
			if err != nil {
				return err
			}
			currentPhase, err := phase.CurrentPhase(sess.Status)
			if err != nil {
				return err
			}
			ui.Success(fmt.Sprintf("Resuming session %s from %s phase", args[0], currentPhase))
			return phase.RunSessionFromPhase(s, sess, currentPhase)
		},
	}
}

func sessionEndCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "end [session-id]",
		Short: "Mark a session as completed",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			id, err := resolveSessionID(s, args)
			if err != nil {
				return err
			}
			if err := session.Complete(s, id); err != nil {
				return err
			}
			ui.Success(fmt.Sprintf("Session %s completed", id))
			return nil
		},
	}
}

func sessionAbandonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "abandon [session-id]",
		Short: "Abandon a session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			id, err := resolveSessionID(s, args)
			if err != nil {
				return err
			}
			proceed, err := ui.Confirm(fmt.Sprintf("Abandon session %s?", id))
			if err != nil {
				return err
			}
			if !proceed {
				ui.Info("Cancelled.")
				return nil
			}
			if err := session.Abandon(s, id); err != nil {
				return err
			}
			// Clean up session directory
			sessDir := s.Path("sessions", id)
			if err := os.RemoveAll(sessDir); err != nil {
				ui.Warning(fmt.Sprintf("Failed to remove session directory: %v", err))
			}
			ui.Success(fmt.Sprintf("Session %s abandoned and cleaned up", id))
			return nil
		},
	}
}

func sessionRetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "retry [session-id]",
		Short: "Retry the current phase of a session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			id, err := resolveSessionID(s, args)
			if err != nil {
				return err
			}
			sess, err := session.Get(s, id)
			if err != nil {
				return err
			}
			currentPhase, err := phase.CurrentPhase(sess.Status)
			if err != nil {
				return err
			}
			ui.Info(fmt.Sprintf("Retrying %s phase for session %s", currentPhase, id))
			return phase.RunSessionFromPhase(s, sess, currentPhase)
		},
	}
}

func comcapCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "comcap [session-id]",
		Short: "Capture git commits for a completed session",
		Long:  "Capture commits made since a session's base commit, link them to decision capsules, and store for recall. Run this after you've committed your work.",
		Example: `  card comcap
  card comcap 20260127-crypto-site-ad0e
  card comcap --force`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			// Resolve session — prefer most recently updated completed session without FinalCommit
			var sessID string
			if len(args) > 0 {
				sessID = args[0]
			} else {
				sessID, err = resolveComcapSession(s)
				if err != nil {
					return err
				}
			}

			sess, err := session.Get(s, sessID)
			if err != nil {
				return err
			}

			// Banner
			comcapBanner(sess.ID)

			// Phase 1: Collect commits from all repos (author-filtered)
			type repoCommits struct {
				repoID   string
				repo     *repo.Repo
				change   *change.Change
				commits  []string
				messages []string
			}
			var collected []repoCommits

			for _, repoID := range sess.Repos {
				r, err := repo.Get(s, repoID)
				if err != nil {
					ui.Warning(fmt.Sprintf("Repo %s not found, skipping", repoID))
					continue
				}

				ch, err := change.Get(s, sess.ID, repoID)
				if err != nil {
					ui.Warning(fmt.Sprintf("No change record for repo %s, skipping", repoID))
					continue
				}

				if ch.FinalCommit != "" && !force {
					ui.Warning(fmt.Sprintf("Commits already captured for %s (use --force to overwrite)", r.Name))
					continue
				}

				if ch.BaseCommit == "" {
					ui.Warning(fmt.Sprintf("No base commit for %s, skipping", r.Name))
					continue
				}

				ui.Status(fmt.Sprintf("Scanning repo: %s (%s)", r.Name, shortSHA(repoID)))

				// Get commits since base, filtered to current author
				commits, messages := gitLogSince(r.LocalPath, ch.BaseCommit, true)
				if len(commits) == 0 {
					fmt.Fprintf(os.Stderr, "  %s\n", ui.Dim("No commits by you since base"))
					continue
				}

				collected = append(collected, repoCommits{
					repoID:   repoID,
					repo:     r,
					change:   ch,
					commits:  commits,
					messages: messages,
				})
			}

			if len(collected) == 0 {
				ui.Info("No commits to capture")
				return nil
			}

			// Phase 2: Display what will be captured
			var allCommits []string
			for _, rc := range collected {
				fmt.Fprintf(os.Stderr, "\n  %s\n", ui.Bold(rc.repo.Name))
				fmt.Fprintf(os.Stderr, "  Base commit:  %s\n", ui.Dim(shortSHA(rc.change.BaseCommit)))
				fmt.Fprintf(os.Stderr, "  HEAD commit:  %s\n", ui.Green(shortSHA(rc.commits[0])))
				fmt.Fprintf(os.Stderr, "  Commits found: %s\n\n", ui.Bold(fmt.Sprintf("%d", len(rc.commits))))

				var rows [][]string
				for i, sha := range rc.commits {
					msg := ""
					if i < len(rc.messages) {
						msg = rc.messages[i]
						if len(msg) > 60 {
							msg = msg[:60] + ".."
						}
					}
					rows = append(rows, []string{shortSHA(sha), msg})
				}
				ui.Table([]string{"SHA", "MESSAGE"}, rows)
				allCommits = append(allCommits, rc.commits...)
			}

			// Phase 3: Confirm before saving (y/n/s for select)
			fmt.Fprintln(os.Stderr)
			response, err := ui.ConfirmOrSelect(fmt.Sprintf("Capture these %d commit(s)?", len(allCommits)))
			if err != nil {
				return err
			}

			if response == "no" {
				ui.Info("Cancelled")
				return nil
			}

			// Handle selection mode
			if response == "select" {
				allCommits = nil // Reset - we'll rebuild from selections
				for i := range collected {
					rc := &collected[i]
					// Build commit items for selection
					items := make([]ui.CommitItem, len(rc.commits))
					for j, sha := range rc.commits {
						msg := ""
						if j < len(rc.messages) {
							msg = rc.messages[j]
						}
						items[j] = ui.CommitItem{SHA: sha, Message: msg, Selected: true}
					}

					selected, err := ui.SelectCommits(rc.repo.Name, items)
					if err != nil {
						return err
					}

					// Filter commits to only selected ones
					var newCommits, newMessages []string
					for _, idx := range selected {
						newCommits = append(newCommits, rc.commits[idx])
						if idx < len(rc.messages) {
							newMessages = append(newMessages, rc.messages[idx])
						}
					}
					rc.commits = newCommits
					rc.messages = newMessages
					allCommits = append(allCommits, newCommits...)
				}

				if len(allCommits) == 0 {
					ui.Info("No commits selected")
					return nil
				}
				fmt.Fprintf(os.Stderr, "\n%s\n", ui.Bold(fmt.Sprintf("Selected %d commit(s) for capture", len(allCommits))))
			}

			// Phase 4: Save changes
			for _, rc := range collected {
				rc.change.FinalCommit = rc.commits[0]
				rc.change.UpdatedAt = sess.UpdatedAt
				if err := change.Save(s, rc.change); err != nil {
					ui.Error(fmt.Sprintf("Failed to save change for %s: %v", rc.repo.Name, err))
					continue
				}
			}

			// Link commits to capsules
			if len(allCommits) > 0 {
				linked, err := capsule.LinkCommitsForSession(s, sess.ID, allCommits)
				if err != nil {
					ui.Warning(fmt.Sprintf("Could not link commits to capsules: %v", err))
				} else if linked > 0 {
					ui.Info(fmt.Sprintf("Linked %d commits to %d decision capsules", len(allCommits), linked))
				}
			}

			fmt.Fprintf(os.Stderr, "\n%s\n",
				ui.Green(fmt.Sprintf("✓ Commit capture complete for session %s", sess.ID)))
			ui.Notify("CARD", fmt.Sprintf("Commits captured for session %s", sess.ID))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite previously captured commits")
	return cmd
}

// resolveComcapSession finds the best session for comcap:
// most recently updated completed session that hasn't had commits captured yet.
func resolveComcapSession(s *store.Store) (string, error) {
	all, err := session.List(s)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		return "", fmt.Errorf("no sessions found")
	}

	// Sort by UpdatedAt descending
	sort.Slice(all, func(i, j int) bool {
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})

	// Prefer completed sessions without FinalCommit
	for _, sess := range all {
		if sess.Status != session.StatusCompleted {
			continue
		}
		// Check if any repo still needs commit capture
		for _, repoID := range sess.Repos {
			ch, err := change.Get(s, sess.ID, repoID)
			if err == nil && ch.FinalCommit == "" {
				return sess.ID, nil
			}
		}
	}

	// Fall back to most recent completed session
	for _, sess := range all {
		if sess.Status == session.StatusCompleted {
			return sess.ID, nil
		}
	}

	// Fall back to most recent active session
	for _, sess := range all {
		if sess.Status != session.StatusAbandoned {
			return sess.ID, nil
		}
	}

	return "", fmt.Errorf("no eligible sessions found for commit capture")
}

// comcapBanner renders the CARD commit capture banner.
func comcapBanner(sessionID string) {
	ui.CommandBanner("COMMIT CAPTURE", fmt.Sprintf("session: %s", sessionID))
}

// gitLogSince returns commit SHAs and first-line messages since baseCommit.
// If authorOnly is true, filters to commits matching the configured git user.email.
func gitLogSince(repoPath, baseCommit string, authorOnly bool) (shas []string, messages []string) {
	args := []string{"-C", repoPath, "log", "--format=%H %s", baseCommit + "..HEAD"}
	if authorOnly {
		email := gitUserEmail(repoPath)
		if email != "" {
			args = append(args, "--author="+email)
		}
	}
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		shas = append(shas, parts[0])
		if len(parts) > 1 {
			messages = append(messages, parts[1])
		} else {
			messages = append(messages, "")
		}
	}
	return
}

// gitUserEmail returns the configured git user.email for a repo.
func gitUserEmail(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "config", "user.email")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// shortSHA returns the first 7 characters of a SHA.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func capsuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capsule",
		Short: "Query and manage decision capsules",
	}
	cmd.AddCommand(capsuleListCmd())
	cmd.AddCommand(capsuleShowCmd())
	cmd.AddCommand(capsuleInvalidateCmd())
	cmd.AddCommand(capsuleVerifyCmd())
	return cmd
}

func capsuleListCmd() *cobra.Command {
	var sessionID, repoID, phase, file, tag string
	var showEvolution bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List decision capsules",
		Long:  "List decision capsules. By default, shows only the latest-phase version of each decision (deduplicates across phases). Use --show-evolution to see all phases.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			f := capsule.Filter{
				ShowEvolution: showEvolution,
			}
			if sessionID != "" {
				f.SessionID = &sessionID
			}
			if repoID != "" {
				f.RepoID = &repoID
			}
			if phase != "" {
				f.Phase = &phase
			}
			if file != "" {
				f.FilePath = &file
			}
			if tag != "" {
				f.Tag = &tag
			}

			caps, err := capsule.List(s, f)
			if err != nil {
				return err
			}
			if len(caps) == 0 {
				ui.EmptyState("No capsules found.")
				return nil
			}

			var rows [][]string
			for _, c := range caps {
				q := c.Question
				if len(q) > 28 {
					q = q[:28] + ".."
				}
				ch := c.Choice
				if len(ch) > 30 {
					ch = ch[:30] + ".."
				}
				rows = append(rows, []string{c.ID, c.Phase, q, ch})
			}
			ui.Table([]string{"ID", "PHASE", "QUESTION", "CHOICE"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "Filter by session ID")
	cmd.Flags().StringVar(&repoID, "repo", "", "Filter by repo ID")
	cmd.Flags().StringVar(&phase, "phase", "", "Filter by phase")
	cmd.Flags().StringVar(&file, "file", "", "Filter by file path tag")
	cmd.Flags().StringVar(&tag, "tag", "", "Filter by tag")
	cmd.Flags().BoolVar(&showEvolution, "show-evolution", false, "Show all phases of each decision (disables deduplication)")
	return cmd
}

func capsuleShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <capsule-id>",
		Short: "Show full details of a decision capsule",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			c, err := capsule.Get(s, args[0])
			if err != nil {
				return err
			}

			ui.SectionHeader("Decision Capsule")
			ui.KeyValue("ID:          ", c.ID)
			ui.KeyValue("Session:     ", c.SessionID)
			ui.KeyValue("Repos:       ", strings.Join(c.RepoIDs, ", "))
			ui.KeyValue("Phase:       ", c.Phase)
			ui.KeyValue("Timestamp:   ", c.Timestamp.Format("2006-01-02 15:04:05"))
			ui.KeyValue("Source:      ", c.Source)
			if c.Status != "" {
				statusStr := string(c.Status)
				switch c.Status {
				case capsule.StatusVerified:
					statusStr = ui.Green(statusStr)
				case capsule.StatusInvalidated:
					statusStr = ui.Red(statusStr)
				case capsule.StatusHypothesis:
					statusStr = ui.Yellow(statusStr)
				}
				ui.KeyValue("Status:      ", statusStr)
			}
			if c.Type != "" {
				ui.KeyValue("Type:        ", string(c.Type))
			}
			ui.SectionHeader("Decision")
			ui.KeyValue("Question:    ", c.Question)
			ui.KeyValue("Choice:      ", ui.Green(c.Choice))
			if len(c.Alternatives) > 0 {
				ui.KeyValue("Alternatives:", ui.Dim(strings.Join(c.Alternatives, ", ")))
			}
			ui.KeyValue("Rationale:   ", c.Rationale)
			if len(c.Tags) > 0 {
				ui.KeyValue("Tags:        ", strings.Join(c.Tags, ", "))
			}
			if len(c.Commits) > 0 {
				ui.KeyValue("Commits:     ", strings.Join(c.Commits, ", "))
			}
			if c.SupersededBy != "" {
				ui.KeyValue("SupersededBy:", ui.Yellow(c.SupersededBy))
			}
			if len(c.Supersedes) > 0 {
				ui.KeyValue("Supersedes:  ", strings.Join(c.Supersedes, ", "))
			}
			if len(c.Challenges) > 0 {
				ui.SectionHeader("Challenges")
				for _, ch := range c.Challenges {
					ui.Detail(ch.Timestamp.Format("2006-01-02"), fmt.Sprintf("%s (%s)", ch.Reason, ch.Resolution))
				}
			}
			return nil
		},
	}
}

func capsuleInvalidateCmd() *cobra.Command {
	var reason string
	var learned string
	var supersededBy string
	var force bool

	cmd := &cobra.Command{
		Use:   "invalidate <capsule-id>",
		Short: "Mark a decision capsule as invalidated",
		Long: `Mark a decision capsule as invalidated, optionally linking to its replacement.

Use this when a prior decision has been proven wrong, outdated, or superseded
by a better approach. The invalidation is recorded in CARD's memory with a
reason and timestamp.

Use --learned to capture what was learned from this invalidation — distinct from
the reason. This creates a learnings database for future reference.

If you're creating a new decision that supersedes this one, use --superseded-by
to link them together for traceability.`,
		Example: `  card capsule invalidate 20260130-auth-abc123 --reason "REST API doesn't scale for our needs"
  card capsule invalidate 20260130-auth-abc123 --reason "Performance issues" --learned "Synchronous APIs don't work for our scale"
  card capsule invalidate 20260130-auth-abc123 --superseded-by 20260130-auth-def456
  card capsule invalidate 20260130-auth-abc123 --force  # Skip confirmation`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			capsuleID := args[0]

			// Get the capsule first to show what we're invalidating
			c, err := capsule.Get(s, capsuleID)
			if err != nil {
				return err
			}

			// Check if already invalidated
			if c.Status == capsule.StatusInvalidated {
				ui.Warning(fmt.Sprintf("Capsule %s is already invalidated", capsuleID))
				if c.SupersededBy != "" {
					ui.Detail("Superseded by:", c.SupersededBy)
				}
				return nil
			}

			// Show capsule details
			ui.SectionHeader("Invalidating Decision")
			ui.KeyValue("ID:      ", c.ID)
			ui.KeyValue("Question:", c.Question)
			ui.KeyValue("Choice:  ", c.Choice)
			fmt.Fprintln(os.Stderr)

			// Require confirmation unless --force
			if !force {
				proceed, err := ui.Confirm("Mark as invalidated?")
				if err != nil {
					return err
				}
				if !proceed {
					ui.Info("Cancelled.")
					return nil
				}
			}

			// Prompt for reason if not provided
			if reason == "" {
				reason = "Manually invalidated via CLI"
			}

			// Perform invalidation
			if err := capsule.Invalidate(s, capsuleID, reason, learned, supersededBy); err != nil {
				return fmt.Errorf("failed to invalidate: %w", err)
			}

			ui.Success(fmt.Sprintf("Capsule %s marked as invalidated", capsuleID))
			if supersededBy != "" {
				ui.Detail("Superseded by:", supersededBy)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Reason for invalidation")
	cmd.Flags().StringVar(&learned, "learned", "", "What was learned from this invalidation")
	cmd.Flags().StringVar(&supersededBy, "superseded-by", "", "Capsule ID of the replacement decision")
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation prompt")
	return cmd
}

func capsuleVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <capsule-id>",
		Short: "Mark a decision capsule as verified",
		Long: `Mark a decision capsule as verified.

Verification confirms that a decision hypothesis has been proven correct
through testing, review, or successful implementation. This increases
confidence in the decision for future recall.`,
		Example: `  card capsule verify 20260130-auth-abc123`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			capsuleID := args[0]

			// Get the capsule first
			c, err := capsule.Get(s, capsuleID)
			if err != nil {
				return err
			}

			// Check if already verified
			if c.Status == capsule.StatusVerified {
				ui.Info(fmt.Sprintf("Capsule %s is already verified", capsuleID))
				return nil
			}

			// Check if invalidated
			if c.Status == capsule.StatusInvalidated {
				ui.Warning(fmt.Sprintf("Capsule %s is invalidated and cannot be verified", capsuleID))
				return nil
			}

			// Perform verification
			if err := capsule.Verify(s, capsuleID); err != nil {
				return fmt.Errorf("failed to verify: %w", err)
			}

			ui.Success(fmt.Sprintf("Capsule %s marked as verified", capsuleID))
			return nil
		},
	}
}

func recallCmd() *cobra.Command {
	var files []string
	var repoID string
	var tags []string
	var format string

	cmd := &cobra.Command{
		Use:   "recall",
		Short: "Query prior CARD context by files, repo, or tags",
		Long:  "Search CARD's memory for prior decisions, sessions, and context related to specific files, repos, or tags. Useful for understanding past work before starting a new session.",
		Example: `  card recall --files src/auth.ts src/middleware.ts
  card recall --repo abc123def456
  card recall --tag authentication --format full`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			// Auto-detect repo from CWD if not specified
			if repoID == "" && len(files) > 0 {
				repoID, _ = detectRepoFromCWD(s)
				if repoID == "" {
					return fmt.Errorf("--repo is required (or run from within a registered repo)")
				}
			}

			q := recall.RecallQuery{
				Files:  files,
				RepoID: repoID,
				Tags:   tags,
			}

			// Set repo path for git correlation
			if repoID != "" {
				r, err := repo.Get(s, repoID)
				if err == nil {
					q.RepoPath = r.LocalPath
				}
			}

			// If no flags and we have a repo, show recent sessions
			if len(files) == 0 && len(tags) == 0 && repoID == "" {
				repoID, _ = detectRepoFromCWD(s)
				if repoID != "" {
					q.RepoID = repoID
				} else {
					return fmt.Errorf("specify --files, --repo, or --tag (or run from within a registered repo)")
				}
			}

			result, err := recall.Query(s, q)
			if err != nil {
				return err
			}

			full := format == "full"
			fmt.Print(recall.FormatTerminal(result, full))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&files, "files", nil, "File paths to search for prior context")
	cmd.Flags().StringVar(&repoID, "repo", "", "Repo ID to search")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "Tags to search for")
	cmd.Flags().StringVar(&format, "format", "brief", "Output format: brief or full")
	return cmd
}

func preflightCmd() *cobra.Command {
	var files []string
	var intent string
	var format string

	cmd := &cobra.Command{
		Use:   "preflight",
		Short: "Get pre-flight briefing for files before editing",
		Long: `Get a pre-flight briefing before working on files.

Combines file context, relevant decisions, and patterns into actionable guidance.
Useful with Claude Code hooks to surface context before file modifications.`,
		Example: `  card preflight --files src/auth/guard.ts
  card preflight --files src/auth/guard.ts --intent "adding rate limiting"
  card preflight --files src/auth.ts src/middleware.ts --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(files) == 0 {
				return fmt.Errorf("at least one --files flag is required")
			}

			s, err := loadStore()
			if err != nil {
				return err
			}

			// Auto-detect repo from CWD
			repoID, _ := detectRepoFromCWD(s)

			// Query capsules for each file
			allCapsules, err := capsule.List(s, capsule.Filter{})
			if err != nil {
				return err
			}

			type fileInfo struct {
				File          string   `json:"file"`
				CapsuleCount  int      `json:"capsule_count"`
				StatusSummary string   `json:"status_summary"`
				Decisions     []string `json:"decisions,omitempty"`
			}

			var fileInfos []fileInfo
			var matchedCapsules []capsule.Capsule

			for _, file := range files {
				var matching []capsule.Capsule
				verified, hypothesis := 0, 0

				for _, c := range allCapsules {
					if c.Status == capsule.StatusInvalidated {
						continue
					}
					if capsule.MatchesTagQuery(c.Tags, "file:"+file) || capsule.MatchesTagQuery(c.Tags, file) {
						matching = append(matching, c)
						matchedCapsules = append(matchedCapsules, c)
						if c.Status == capsule.StatusVerified {
							verified++
						} else {
							hypothesis++
						}
					}
				}

				statusParts := []string{}
				if verified > 0 {
					statusParts = append(statusParts, fmt.Sprintf("%d verified", verified))
				}
				if hypothesis > 0 {
					statusParts = append(statusParts, fmt.Sprintf("%d hypothesis", hypothesis))
				}

				var decisions []string
				for _, c := range matching {
					decisions = append(decisions, fmt.Sprintf("[%s] %s → %s", c.ID[:12], c.Question, c.Choice))
				}

				fi := fileInfo{
					File:          file,
					CapsuleCount:  len(matching),
					StatusSummary: strings.Join(statusParts, ", "),
				}
				if format == "json" {
					fi.Decisions = decisions
				}
				fileInfos = append(fileInfos, fi)
			}

			// Search by intent if provided
			var intentMatches []capsule.Capsule
			if intent != "" {
				intentLower := strings.ToLower(intent)
				for _, c := range allCapsules {
					if c.Status == capsule.StatusInvalidated {
						continue
					}
					// Simple text search in question, choice, rationale, tags
					searchText := strings.ToLower(c.Question + " " + c.Choice + " " + c.Rationale + " " + strings.Join(c.Tags, " "))
					if strings.Contains(searchText, intentLower) {
						// Avoid duplicates
						isDupe := false
						for _, mc := range matchedCapsules {
							if mc.ID == c.ID {
								isDupe = true
								break
							}
						}
						if !isDupe {
							intentMatches = append(intentMatches, c)
						}
					}
				}
			}

			if format == "json" {
				type jsonOutput struct {
					Files         []fileInfo `json:"files"`
					IntentMatches int        `json:"intent_matches,omitempty"`
					RepoID        string     `json:"repo_id,omitempty"`
				}
				out := jsonOutput{
					Files:         fileInfos,
					IntentMatches: len(intentMatches),
					RepoID:        repoID,
				}
				data, _ := json.MarshalIndent(out, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			// Text output
			ui.SectionHeader("Pre-flight Briefing")
			fmt.Println()

			for _, fi := range fileInfos {
				if fi.CapsuleCount == 0 {
					ui.Detail(fi.File, "No prior decisions")
				} else {
					ui.Detail(fi.File, fmt.Sprintf("%d decisions (%s)", fi.CapsuleCount, fi.StatusSummary))
				}
			}

			if len(matchedCapsules) > 0 {
				fmt.Println()
				ui.Info("Relevant decisions:")
				for _, c := range matchedCapsules {
					fmt.Printf("  [%s] %s\n", c.ID[:12], c.Question)
					fmt.Printf("    → %s\n", c.Choice)
				}
			}

			if len(intentMatches) > 0 {
				fmt.Println()
				ui.Info(fmt.Sprintf("Intent matches (%d):", len(intentMatches)))
				for _, c := range intentMatches {
					fmt.Printf("  [%s] %s\n", c.ID[:12], c.Question)
				}
			}

			if len(matchedCapsules) == 0 && len(intentMatches) == 0 {
				fmt.Println()
				ui.Info("No prior decisions found. This appears to be new territory.")
			}

			return nil
		},
	}
	cmd.Flags().StringArrayVar(&files, "files", nil, "File paths to get context for")
	cmd.Flags().StringVar(&intent, "intent", "", "What you're planning to do (e.g., 'adding rate limiting')")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json")
	_ = cmd.MarkFlagRequired("files")
	return cmd
}

// detectRepoFromCWD checks if the current directory is inside a registered repo.
func detectRepoFromCWD(s *store.Store) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	repos, err := repo.List(s)
	if err != nil {
		return "", err
	}

	for _, r := range repos {
		if strings.HasPrefix(cwd, r.LocalPath) {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("not inside a registered repo")
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and edit CARD configuration",
	}
	cmd.AddCommand(configShowCmd())
	cmd.AddCommand(configSetCmd())
	return cmd
}

func configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Display current effective configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			data, err := yaml.Marshal(s.Config)
			if err != nil {
				return fmt.Errorf("failed to marshal config: %w", err)
			}
			fmt.Print(string(data))
			return nil
		},
	}
}

func configSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Long:  "Set a CARD configuration value. Valid keys: claude.path, session.auto_continue_simplify, session.auto_continue_record, recall.max_context_blocks, recall.max_context_chars.",
		Example: `  card config set claude.path /usr/local/bin/claude
  card config set session.auto_continue_simplify false
  card config set recall.max_context_blocks 15`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			if err := s.SetConfigValue(args[0], args[1]); err != nil {
				return err
			}
			ui.Success(fmt.Sprintf("Set %s = %s", args[0], args[1]))
			return nil
		},
	}
}

func doctorCmd() *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check health of CARD store and registered repos",
		RunE: func(cmd *cobra.Command, args []string) error {
			home := store.Home()

			s, err := store.Load(home)
			if err != nil {
				return fmt.Errorf("CARD not initialized — run 'card init' first: %w", err)
			}

			if fix {
				ui.CommandBanner("DOCTOR", "repair mode")
				fixed := store.FixIssues(home)
				for _, f := range fixed {
					ui.Success(fmt.Sprintf("[FIXED] %s", f))
				}

				// Recover orphaned artifacts
				orphans, err := phase.CheckOrphanedArtifacts(s)
				if err != nil {
					ui.Warning(fmt.Sprintf("Failed to check for orphaned artifacts: %v", err))
				} else {
					for _, orphan := range orphans {
						ui.Info(fmt.Sprintf("Recovering orphaned %s artifact for session %s...", orphan.Phase, orphan.SessionID))
						if err := phase.RecoverOrphanedArtifact(s, orphan); err != nil {
							ui.Error(fmt.Sprintf("Failed to recover: %v", err))
						} else {
							ui.Success(fmt.Sprintf("[FIXED] Recovered %s artifact for session %s", orphan.Phase, orphan.SessionID))
							fixed = append(fixed, fmt.Sprintf("Recovered %s artifact for session %s", orphan.Phase, orphan.SessionID))
						}
					}
				}

				// Clean stale ephemeral artifacts from completed sessions
				cleanupResult := store.CleanEphemeralArtifacts(home)
				for _, c := range cleanupResult.Messages {
					ui.Success(fmt.Sprintf("[FIXED] %s", c))
				}
				fixed = append(fixed, cleanupResult.Messages...)

				// Regenerate session summaries for affected sessions (remove stale artifact links)
				for _, sessionID := range cleanupResult.AffectedSessionIDs {
					if err := session.RegenerateSummary(s, sessionID); err == nil {
						ui.Success(fmt.Sprintf("[FIXED] session %s: regenerated summary", sessionID))
						fixed = append(fixed, fmt.Sprintf("session %s: regenerated summary", sessionID))
					}
				}

				if len(fixed) == 0 {
					ui.EmptyState("Nothing to fix.")
				}
			}

			if !fix {
				ui.CommandBanner("DOCTOR", "health check")
			}

			// Collect all issues
			issues := store.CheckHealth(home)
			repoIssues, err := repo.CheckAllHealth(s)
			if err != nil {
				return err
			}
			issues = append(issues, repoIssues...)
			issues = append(issues, store.CheckSessionIntegrity(home)...)
			issues = append(issues, store.CheckCapsuleIntegrity(home)...)

			// Check for orphaned artifacts
			orphans, err := phase.CheckOrphanedArtifacts(s)
			if err == nil && len(orphans) > 0 {
				for _, orphan := range orphans {
					issues = append(issues, store.Issue{
						Severity: "warning",
						Message:  fmt.Sprintf("session %s: orphaned %s artifact in temp (run 'card doctor --fix' to recover)", orphan.SessionID, orphan.Phase),
					})
				}
			}

			// Check for stale ephemeral artifacts in completed sessions
			staleArtifacts := store.CheckEphemeralArtifacts(home)
			if len(staleArtifacts) > 0 {
				for _, artifact := range staleArtifacts {
					issues = append(issues, store.Issue{
						Severity: "warning",
						Message:  fmt.Sprintf("session %s: stale ephemeral artifact %s (run 'card doctor --fix' to clean)", artifact.SessionID, artifact.Filename),
					})
				}
			}

			// Check Claude Code availability
			if err := claude.AvailableAt(s.Config.Claude.Path); err != nil {
				issues = append(issues, store.Issue{Severity: "warning", Message: fmt.Sprintf("Claude Code: %v", err)})
			}

			if len(issues) == 0 {
				ui.Success("Everything looks good")
				os.Exit(0)
			}

			hasError := false
			for _, issue := range issues {
				if issue.Severity == "error" {
					ui.Error(fmt.Sprintf("[ERR]  %s", issue.Message))
					hasError = true
				} else {
					ui.Warning(fmt.Sprintf("[WARN] %s", issue.Message))
				}
			}

			if hasError {
				os.Exit(2)
			}
			os.Exit(1)
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "Repair issues, recover orphaned artifacts, and clean stale ephemeral artifacts from completed sessions")
	return cmd
}

func cleanCmd() *cobra.Command {
	var dryRun bool
	var all bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove abandoned and completed session data",
		Long:  "Removes session directories for abandoned sessions. Use --all to also remove completed sessions.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}
			sessions, err := session.List(s)
			if err != nil {
				return err
			}
			var toClean []session.Session
			for _, sess := range sessions {
				if sess.Status == session.StatusAbandoned {
					toClean = append(toClean, sess)
				}
				if all && sess.Status == session.StatusCompleted {
					toClean = append(toClean, sess)
				}
			}
			if len(toClean) == 0 {
				ui.EmptyState("Nothing to clean.")
				return nil
			}
			for _, sess := range toClean {
				sessDir := s.Path("sessions", sess.ID)
				if dryRun {
					ui.Detail("Would remove:", fmt.Sprintf("%s %s", sess.ID, ui.Dim("("+string(sess.Status)+")")))
				} else {
					if err := os.RemoveAll(sessDir); err != nil {
						ui.Warning(fmt.Sprintf("Failed to remove %s: %v", sess.ID, err))
					} else {
						ui.Success(fmt.Sprintf("Removed %s %s", sess.ID, ui.Dim("("+string(sess.Status)+")")))
					}
				}
			}
			if dryRun {
				ui.Info(fmt.Sprintf("%d session(s) would be removed. Run without --dry-run to proceed.", len(toClean)))
			} else {
				ui.Success(fmt.Sprintf("Cleaned %d session(s)", len(toClean)))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what would be removed")
	cmd.Flags().BoolVar(&all, "all", false, "Also remove completed sessions")
	return cmd
}

func completionCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish]",
		Short:     "Generate shell completion scripts",
		Long:      "Generate shell completion scripts for bash, zsh, or fish. Output the script to stdout for sourcing in your shell profile.",
		Example:   "  card completion bash > ~/.bashrc.d/card\n  card completion zsh > ~/.zfunc/_card\n  card completion fish > ~/.config/fish/completions/card.fish",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			default:
				return fmt.Errorf("unsupported shell: %s (use bash, zsh, or fish)", args[0])
			}
		},
	}
}

func exportCmd() *cobra.Command {
	var outputPath string
	cmd := &cobra.Command{
		Use:   "export <session-id>",
		Short: "Export a session to a portable .card bundle",
		Long: `Export a CARD session to a portable .card bundle file.

The bundle contains all session data, artifacts, and decision capsules.
It can be shared with teammates and imported into their CARD installation.

The bundle includes repo metadata for automatic re-linking on import.`,
		Example: `  card export 20260130-add-auth-abc123
  card export 20260130-add-auth-abc123 -o ~/Desktop/auth-session.card`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			sessionID := args[0]

			// Determine output path
			outPath := outputPath
			if outPath == "" {
				outPath = fmt.Sprintf("%s.card", sessionID)
			}

			ui.Status(fmt.Sprintf("Exporting session %s...", sessionID))

			if err := bundle.Export(s, sessionID, outPath); err != nil {
				return fmt.Errorf("export failed: %w", err)
			}

			// Get file info for size display
			info, _ := os.Stat(outPath)
			sizeStr := ""
			if info != nil {
				sizeStr = fmt.Sprintf(" (%d bytes)", info.Size())
			}

			ui.Success(fmt.Sprintf("Exported to %s%s", outPath, sizeStr))
			return nil
		},
	}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output file path (default: <session-id>.card)")
	return cmd
}

func importCmd() *cobra.Command {
	var preview bool
	cmd := &cobra.Command{
		Use:   "import <bundle-path>",
		Short: "Import a session from a .card bundle",
		Long: `Import a CARD session from a .card bundle file.

The bundle is extracted into your CARD_HOME. If the bundle references
repositories that you have registered locally (matching by remote URL),
they will be automatically linked.

Use --preview to see what will be imported without making changes.`,
		Example: `  card import auth-session.card
  card import ~/Downloads/teammate-session.card --preview`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bundlePath := args[0]

			// Preview mode
			if preview {
				manifest, err := bundle.ReadManifest(bundlePath)
				if err != nil {
					return fmt.Errorf("failed to read bundle: %w", err)
				}

				ui.CommandBanner("IMPORT PREVIEW", bundlePath)
				ui.KeyValue("Session ID:  ", manifest.SessionID)
				ui.KeyValue("Description: ", manifest.Description)
				ui.KeyValue("Exported by: ", manifest.ExportedBy)
				ui.KeyValue("Exported at: ", manifest.ExportedAt.Format("2006-01-02 15:04:05"))
				ui.KeyValue("Files:       ", fmt.Sprintf("%d", len(manifest.Files)))

				if len(manifest.Repos) > 0 {
					ui.SectionHeader("Repositories")
					for _, r := range manifest.Repos {
						if r.RemoteURL != "" {
							ui.Detail(r.Name, ui.Dim(r.RemoteURL))
						} else {
							ui.Detail(r.ID, ui.Dim("(no remote URL)"))
						}
					}
				}

				ui.Info("Use 'card import' without --preview to import this session.")
				return nil
			}

			s, err := loadStore()
			if err != nil {
				return err
			}

			ui.Status(fmt.Sprintf("Importing from %s...", bundlePath))

			result, err := bundle.Import(s, bundlePath)
			if err != nil {
				return fmt.Errorf("import failed: %w", err)
			}

			ui.Success(fmt.Sprintf("Imported session %s", result.SessionID))
			ui.KeyValue("Description:    ", result.Description)
			ui.KeyValue("Original author:", result.OriginalAuthor)
			ui.KeyValue("Files imported: ", fmt.Sprintf("%d", result.FilesImported))

			if len(result.LinkedRepos) > 0 {
				ui.SectionHeader("Linked Repositories")
				for _, id := range result.LinkedRepos {
					r, _ := repo.Get(s, id)
					if r != nil {
						ui.Detail(r.Name, ui.Green("linked"))
					} else {
						ui.Detail(id, ui.Green("linked"))
					}
				}
			}

			if len(result.UnlinkedRepos) > 0 {
				ui.SectionHeader("Unlinked Repositories")
				ui.Warning("These repos were not found locally. Register them with 'card repo add' to link:")
				for _, remote := range result.UnlinkedRepos {
					ui.Detail("", remote)
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVar(&preview, "preview", false, "Preview bundle contents without importing")
	return cmd
}

func mcpServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "mcp-serve",
		Short:  "Run CARD as an MCP server",
		Long:   "Start CARD as a Model Context Protocol (MCP) server over stdio. This allows Claude Code and other MCP-compatible tools to query CARD's engineering memory directly.",
		Hidden: true, // Not typically called directly by users
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			server := cardmcp.NewServer(s, version)
			return server.Run(context.Background())
		},
	}
}

func askCmd() *cobra.Command {
	var repoPath string
	var setupMCP bool

	cmd := &cobra.Command{
		Use:   "ask",
		Short: "Start an interactive conversation with CARD context",
		Long: `Start Claude Code with access to CARD's engineering memory.

Claude can query prior decisions, session history, and context as the conversation
evolves. No need to specify files or tags upfront — just ask questions and Claude
will pull relevant context from CARD as needed.

On first run, this command will configure Claude Code to use CARD's MCP server.`,
		Example: `  card ask                    # Start from current directory
  card ask --repo /path/to/repo  # Start in a specific repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := loadStore()
			if err != nil {
				return err
			}

			// Determine working directory
			workDir := repoPath
			if workDir == "" {
				workDir, _ = os.Getwd()
			}

			// Check if we're in a registered repo
			var repoID string
			var repoName string
			repos, _ := repo.List(s)
			for _, r := range repos {
				if strings.HasPrefix(workDir, r.LocalPath) {
					repoID = r.ID
					repoName = r.Name
					workDir = r.LocalPath
					break
				}
			}

			// Force MCP reconfiguration if --setup-mcp flag is set
			// (normal setup already happens in PersistentPreRun)
			if setupMCP {
				if err := ensureMCPConfigured(true); err != nil {
					return err
				}
			}

			// Bootstrap context: fetch recent decisions and sessions
			bootstrap := &askBootstrapContext{
				RepoName: repoName,
			}

			// Fetch recent decisions (silent fail - don't block ask on recall errors)
			recallResult, err := recall.Query(s, recall.RecallQuery{
				RepoID:      repoID,
				RepoPath:    workDir,
				MaxCapsules: 15,
				RecentOnly:  true,
			})
			if err == nil && recallResult != nil {
				bootstrap.RecentDecisions = recallResult.Capsules
				bootstrap.RecentSessions = recallResult.Sessions
			}

			// Build system prompt with bootstrapped context
			systemPrompt := buildAskSystemPrompt(repoID, workDir, bootstrap)

			// Display CARD logo with wizard and wisdom (animated Pensieve intro)
			ui.AnimatedWisdomBanner()
			ui.Status("Starting Claude Code with CARD context...")
			fmt.Fprintln(os.Stderr)

			return claude.Invoke(claude.InvokeOptions{
				SystemPrompt: systemPrompt,
				WorkingDir:   workDir,
				Mode:         claude.ModeInteractive,
			})
		},
	}

	cmd.Flags().StringVar(&repoPath, "repo", "", "Repository path to work in")
	cmd.Flags().BoolVar(&setupMCP, "setup-mcp", false, "Force reconfiguration of Claude Code MCP settings")

	return cmd
}

// ensureMCPConfigured checks and configures Claude Code's MCP settings for CARD.
// It reads ~/.claude.json directly for speed (instead of calling `claude mcp list`).
// It auto-removes the alternate MCP server (card vs card-dev) to avoid confusion.
func ensureMCPConfigured(force bool) error {
	// Find the card binary path and determine MCP server name
	cardPath, err := os.Executable()
	if err != nil {
		cardPath = "card" // Fall back to PATH lookup
	}

	// Resolve symlinks to get the actual binary path
	if resolved, err := filepath.EvalSymlinks(cardPath); err == nil {
		cardPath = resolved
	}

	// Use binary name as MCP server name (allows card-dev alongside card)
	mcpName := filepath.Base(cardPath)
	if mcpName == "" {
		mcpName = "card"
	}

	// Determine the alternate name (card <-> card-dev)
	alternateName := "card"
	if mcpName == "card" {
		alternateName = "card-dev"
	}

	// Read Claude's MCP config directly for speed
	// Note: MCP servers are stored in ~/.claude.json (not ~/.claude/settings.json)
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil // Silently skip if we can't find home dir
	}
	settingsPath := filepath.Join(homeDir, ".claude.json")

	type mcpServerConfig struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	type claudeSettings struct {
		MCPServers map[string]mcpServerConfig `json:"mcpServers"`
	}

	var settings claudeSettings
	settingsData, err := os.ReadFile(settingsPath)
	if err == nil {
		// Parse existing settings
		if err := json.Unmarshal(settingsData, &settings); err != nil {
			settings = claudeSettings{MCPServers: make(map[string]mcpServerConfig)}
		}
	} else {
		settings = claudeSettings{MCPServers: make(map[string]mcpServerConfig)}
	}

	if settings.MCPServers == nil {
		settings.MCPServers = make(map[string]mcpServerConfig)
	}

	// Check if already correctly configured
	if !force {
		if existing, ok := settings.MCPServers[mcpName]; ok {
			if existing.Command == cardPath {
				// Already configured with correct path
				// Still check if we need to remove the alternate
				if _, hasAlternate := settings.MCPServers[alternateName]; hasAlternate {
					// Remove alternate from user scope (where we configure)
					// Also try project scope in case it exists there too
					exec.Command("claude", "mcp", "remove", alternateName, "-s", "user").Run()
					exec.Command("claude", "mcp", "remove", alternateName, "-s", "local").Run()
					ui.Success(fmt.Sprintf("Removed conflicting MCP server (%s)", alternateName))
				}
				return nil
			}
		}
	}

	// Remove alternate MCP server if it exists (card vs card-dev)
	if _, hasAlternate := settings.MCPServers[alternateName]; hasAlternate {
		exec.Command("claude", "mcp", "remove", alternateName, "-s", "user").Run()
		exec.Command("claude", "mcp", "remove", alternateName, "-s", "local").Run()
	}

	// Remove existing server if force reconfiguring or if path changed
	if existing, ok := settings.MCPServers[mcpName]; ok {
		if force || existing.Command != cardPath {
			exec.Command("claude", "mcp", "remove", mcpName, "-s", "user").Run()
			exec.Command("claude", "mcp", "remove", mcpName, "-s", "local").Run()
		}
	}

	// Add CARD MCP server using Claude's native command
	// Use --scope user to make it available globally
	cmd := exec.Command("claude", "mcp", "add", "--scope", "user", mcpName, "--", cardPath, "mcp-serve")
	if output, err := cmd.CombinedOutput(); err != nil {
		// Silently fail - MCP config is nice-to-have, not critical
		_ = output
		return nil
	}

	ui.Success(fmt.Sprintf("Configured Claude Code to use CARD MCP server (%s)", mcpName))
	return nil
}

// askBootstrapContext holds pre-fetched context for agent onboarding.
type askBootstrapContext struct {
	RecentDecisions []recall.ScoredCapsule
	RecentSessions  []recall.SessionSummary
	RepoName        string
}

// buildAskSystemPrompt creates a system prompt that tells Claude about CARD.
func buildAskSystemPrompt(repoID, workDir string, bootstrap *askBootstrapContext) string {
	var b strings.Builder

	// === Identity & Philosophy ===
	b.WriteString("# You are a CARD-Aware Agent\n\n")
	b.WriteString("You have access to CARD (Context Artifact Relay Development), the developer's engineering memory system. ")
	b.WriteString("CARD tracks decision capsules — structured records of WHY things were built the way they are.\n\n")

	b.WriteString("## Your Operating Philosophy: PUSH, DON'T PULL\n\n")
	b.WriteString("Your job is to surface relevant context BEFORE the developer needs to ask. Don't wait to be prompted — proactively:\n")
	b.WriteString("- Mention prior decisions that relate to what's being discussed\n")
	b.WriteString("- Warn if a proposal conflicts with established patterns\n")
	b.WriteString("- Reference specific sessions when explaining history\n")
	b.WriteString("- Offer to create quickfix sessions when you discover issues\n\n")

	// === Tool Guidance (Tiered) ===
	b.WriteString("## CARD Tools\n\n")

	b.WriteString("**Start Here — Core Tools:**\n")
	b.WriteString("- `card_recall`: Search decisions by files, tags, or keywords. Call with NO PARAMS to get recent decisions.\n")
	b.WriteString("- `card_preflight(files, intent)`: Pre-work briefing before touching files. Combines context + patterns.\n")
	b.WriteString("- `card_file_context(files)`: Get all decisions related to specific files.\n\n")

	b.WriteString("**Deep Dive — When You Need More:**\n")
	b.WriteString("- `card_capsule_show(id)`: Full details of a specific decision\n")
	b.WriteString("- `card_sessions`: List all work sessions\n")
	b.WriteString("- `card_session_summary(id)`: Quick catch-up on a session\n")
	b.WriteString("- `card_session_artifacts(id)`: Full execution logs and plans (can be 500+ lines)\n")
	b.WriteString("- `card_patterns`: Established implementation patterns in this codebase\n")
	b.WriteString("- `card_hotspots`: Find areas with most decisions (context-rich vs sparse)\n\n")

	b.WriteString("**Recording & Capture:**\n")
	b.WriteString("- `card_record`: Capture a decision mid-conversation (survives session crashes)\n")
	b.WriteString("- `card_quickfix_start`: Promote a discovered issue to a recorded fix session\n\n")

	// === Quickfix Guidance ===
	b.WriteString("## Quickfix Sessions\n\n")
	b.WriteString("If you discover something fixable (bug, security issue, inconsistency), proactively offer to create a quickfix session. ")
	b.WriteString("This records the fix with decision capture while skipping investigation/planning (since you've already done that discovery together). ")
	b.WriteString("Don't wait for the user to ask — if you find something, offer it.\n\n")

	// === Bootstrapped Context ===
	b.WriteString("## Current Context\n\n")

	if bootstrap != nil && bootstrap.RepoName != "" {
		b.WriteString(fmt.Sprintf("**Repository:** %s\n", bootstrap.RepoName))
	} else if repoID != "" {
		b.WriteString(fmt.Sprintf("**Repository ID:** %s\n", repoID))
	}
	b.WriteString(fmt.Sprintf("**Working Directory:** %s\n\n", workDir))

	// Embed recent sessions
	if bootstrap != nil && len(bootstrap.RecentSessions) > 0 {
		b.WriteString("### Recent Sessions\n\n")
		for _, sess := range bootstrap.RecentSessions {
			status := sess.Status
			if status == "completed" {
				status = "✓"
			} else if status == "started" {
				status = "→"
			}
			b.WriteString(fmt.Sprintf("- [%s] **%s** (%s)\n", status, sess.Description, sess.ID))
		}
		b.WriteString("\n")
	}

	// Embed recent decisions
	if bootstrap != nil && len(bootstrap.RecentDecisions) > 0 {
		b.WriteString("### Recent Decisions\n\n")
		b.WriteString("These are the most recent decisions in this codebase. Reference them when relevant:\n\n")
		for i, cap := range bootstrap.RecentDecisions {
			if i >= 10 { // Limit to 10 in preamble
				b.WriteString(fmt.Sprintf("... and %d more (use `card_recall` to explore)\n", len(bootstrap.RecentDecisions)-10))
				break
			}
			b.WriteString(fmt.Sprintf("- **%s** → %s", cap.Question, cap.Choice))
			if len(cap.Tags) > 0 {
				b.WriteString(fmt.Sprintf(" [%s]", strings.Join(cap.Tags[:min(3, len(cap.Tags))], ", ")))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("*No recent decisions found. This may be a new CARD installation or the repo has no recorded sessions yet.*\n\n")
	}

	b.WriteString("---\n")
	b.WriteString("You are now ready to assist. Surface context proactively and help the developer navigate their engineering history.\n")

	return b.String()
}

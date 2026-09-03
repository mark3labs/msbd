package main

// admin.go — the `msbd users`, `msbd keys` and `msbd db` command trees.
//
// These operate on the SAME database file the running daemon reads, so
// `msbd keys create` on the host takes effect in a live server without any IPC:
// SQLite's file locking handles the concurrent access, and the server's
// verification cache picks the change up within its (short) TTL.
//
// Everything here is styled by fang via the shared root command; handlers only
// return errors and write plain, pipe-friendly tables to stdout.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mark3labs/msbd/internal/store"
)

// openStore resolves the data directory and opens (creating + migrating) the
// database. Every admin subcommand starts here.
func openStore(dataDir string) (*store.Store, error) {
	return store.Open(store.DBPath(dataDir))
}

// withStore runs fn against an opened store, closing it afterwards.
func withStore(dataDir string, fn func(context.Context, *store.Store) error) error {
	st, err := openStore(dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(context.Background(), st)
}

// ---------------------------------------------------------------------------
// users
// ---------------------------------------------------------------------------

func newUsersCmd(dataDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Manage dashboard users",
		Long: `Manage the dashboard user accounts stored in msbd's database.

Creating the first user switches the dashboard from HTTP Basic auth (or no auth)
to a real login page with server-side sessions. Passwords are bcrypt-hashed and
never stored, logged or printed.

Users are also manageable from the dashboard itself at /settings/users.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newUsersListCmd(dataDir),
		newUsersAddCmd(dataDir),
		newUsersPasswdCmd(dataDir),
		newUsersRoleCmd(dataDir),
		newUsersRemoveCmd(dataDir),
	)
	return cmd
}

func newUsersListCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List dashboard users",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				users, err := st.ListUsers(ctx)
				if err != nil {
					return err
				}
				if len(users) == 0 {
					outln(cmd.OutOrStdout(),
						"No users yet. Create one with: msbd users add <username>")
					return nil
				}
				tw := newTable(cmd.OutOrStdout(), "USERNAME", "ROLE", "CREATED", "LAST LOGIN")
				for _, u := range users {
					outf(tw, "%s\t%s\t%s\t%s\n",
						u.Username, u.Role, shortTime(u.CreatedAt), shortTime(u.LastLoginAt))
				}
				return tw.Flush()
			})
		},
	}
}

func newUsersAddCmd(dataDir *string) *cobra.Command {
	var role string
	var stdinPass bool
	cmd := &cobra.Command{
		Use:   "add <username>",
		Short: "Create a dashboard user",
		Long: `Create a dashboard user.

The password is read interactively (with confirmation) unless --password-stdin
is given. There is deliberately no --password flag: it would expose the secret
in the process list and the shell history.`,
		Example: `  msbd users add alice
  msbd users add ci --role viewer
  echo "$PASSWORD" | msbd users add bot --password-stdin`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pw, err := readPassword(cmd, stdinPass, "Password: ", true)
			if err != nil {
				return err
			}
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				u, err := st.CreateUser(ctx, args[0], pw, role)
				if err != nil {
					return err
				}
				outf(cmd.OutOrStdout(), "Created user %s (%s).\n", u.Username, u.Role)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&role, "role", store.RoleAdmin,
		"Role: admin (full control) or viewer (read-only dashboard)")
	cmd.Flags().BoolVar(&stdinPass, "password-stdin", false,
		"Read the password from stdin instead of prompting")
	return cmd
}

func newUsersPasswdCmd(dataDir *string) *cobra.Command {
	var stdinPass bool
	cmd := &cobra.Command{
		Use:   "passwd <username>",
		Short: "Change a user's password",
		Long: `Change a user's password.

Every existing browser session for that user is invalidated, so this is also how
you evict a compromised login.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pw, err := readPassword(cmd, stdinPass, "New password: ", true)
			if err != nil {
				return err
			}
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				if err := st.SetPassword(ctx, args[0], pw); err != nil {
					return err
				}
				outf(cmd.OutOrStdout(),
					"Password updated for %s; existing sessions signed out.\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&stdinPass, "password-stdin", false,
		"Read the password from stdin instead of prompting")
	return cmd
}

func newUsersRoleCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:       "role <username> <admin|viewer>",
		Short:     "Change a user's role",
		Args:      cobra.ExactArgs(2),
		ValidArgs: []string{store.RoleAdmin, store.RoleViewer},
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				if err := st.SetRole(ctx, args[0], args[1]); err != nil {
					return err
				}
				outf(cmd.OutOrStdout(), "%s is now %s.\n", args[0], args[1])
				return nil
			})
		},
	}
}

func newUsersRemoveCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <username>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete a dashboard user",
		Long: `Delete a dashboard user and all of their sessions.

Removing the last admin is refused: it would make the dashboard permanently
unreachable.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				if err := st.DeleteUser(ctx, args[0]); err != nil {
					return err
				}
				outf(cmd.OutOrStdout(), "Deleted user %s.\n", args[0])
				return nil
			})
		},
	}
}

// ---------------------------------------------------------------------------
// keys
// ---------------------------------------------------------------------------

func newKeysCmd(dataDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage REST API keys",
		Long: `Manage the bearer tokens stored in msbd's database.

Stored keys are accepted IN ADDITION to any --api-key / MSBD_API_KEY value, so
adopting them never breaks an existing deployment. Creating the first key
switches an otherwise-open server to authenticated.

Only a SHA-256 hash of each token is stored: the token itself is shown once, at
creation, and cannot be recovered afterwards.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		newKeysListCmd(dataDir),
		newKeysCreateCmd(dataDir),
		newKeysRevokeCmd(dataDir),
		newKeysRemoveCmd(dataDir),
	)
	return cmd
}

func newKeysListCmd(dataDir *string) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List API keys",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				keys, err := st.ListAPIKeys(ctx)
				if err != nil {
					return err
				}
				shown := 0
				tw := newTable(cmd.OutOrStdout(),
					"ID", "NAME", "PREFIX", "STATUS", "CREATED", "LAST USED", "EXPIRES")
				for _, k := range keys {
					if !all && !k.Active() {
						continue
					}
					shown++
					outf(tw, "%d\t%s\t%s…\t%s\t%s\t%s\t%s\n",
						k.ID, k.Name, k.Prefix, k.Status(),
						shortTime(k.CreatedAt), shortTime(k.LastUsedAt), expiryText(k.ExpiresAt))
				}
				if shown == 0 {
					hint := "No API keys yet. Create one with: msbd keys create <name>"
					if !all && len(keys) > 0 {
						hint = "No active API keys (use --all to include revoked and expired ones)."
					}
					outln(cmd.OutOrStdout(), hint)
					return nil
				}
				return tw.Flush()
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include revoked and expired keys")
	return cmd
}

func newKeysCreateCmd(dataDir *string) *cobra.Command {
	var expires string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an API key and print the token once",
		Long: `Create an API key.

The token is printed ONCE and never recoverable afterwards — only its SHA-256
hash is stored. Use --quiet to print the bare token for piping into a file or a
secret manager.`,
		Example: `  msbd keys create ci-runner
  msbd keys create temp --expires 30d
  msbd keys create bot --quiet > /run/secrets/msbd-key`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ttl, err := parseExpiry(expires)
			if err != nil {
				return err
			}
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				k, raw, err := st.CreateAPIKey(ctx, args[0], ttl, "cli")
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				if quiet {
					outln(out, raw)
					return nil
				}
				outf(out, "Created API key %q (id %d).\n\n", k.Name, k.ID)
				outf(out, "  %s\n\n", raw)
				outln(out, "This is the only time the token is shown — store it now.")
				if !k.ExpiresAt.IsZero() {
					outf(out, "It expires %s.\n", k.ExpiresAt.Format(time.RFC1123))
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&expires, "expires", "",
		"Lifetime, e.g. 30d, 12h, 90m; empty = never expires")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Print only the token (for scripts)")
	return cmd
}

func newKeysRevokeCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <name|id|prefix>",
		Short: "Disable an API key, keeping its audit record",
		Long: `Disable an API key without deleting it.

The row survives so you keep the record of who created it and when it was last
used. Use "msbd keys rm" to erase it entirely.

The key can be named by its name, its msbd_… prefix, or its numeric id; a name
shared by several keys is refused rather than guessed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				k, err := st.RevokeAPIKey(ctx, args[0])
				if err != nil {
					return err
				}
				outf(cmd.OutOrStdout(), "Revoked key %q (id %d, %s…).\n", k.Name, k.ID, k.Prefix)
				return nil
			})
		},
	}
}

func newKeysRemoveCmd(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name|id|prefix>",
		Aliases: []string{"remove", "delete"},
		Short:   "Delete an API key permanently",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
				k, err := st.DeleteAPIKey(ctx, args[0])
				if err != nil {
					return err
				}
				outf(cmd.OutOrStdout(), "Deleted key %q (id %d).\n", k.Name, k.ID)
				return nil
			})
		},
	}
}

// ---------------------------------------------------------------------------
// db
// ---------------------------------------------------------------------------

func newDBCmd(dataDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Inspect and maintain the msbd database",
		Long: `Inspect and maintain the SQLite database holding users, API keys and sessions.

The daemon migrates the database automatically at startup; these commands exist
for operators who want to do it explicitly, find the file for a backup, or clear
out stale sessions.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the resolved database path",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				outln(cmd.OutOrStdout(), store.DBPath(*dataDir))
				return nil
			},
		},
		&cobra.Command{
			Use:   "migrate",
			Short: "Create or migrate the database, then exit",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
					users, _ := st.CountUsers(ctx)
					keys, _ := st.CountActiveAPIKeys(ctx)
					outf(cmd.OutOrStdout(),
						"Database ready at %s (%d users, %d active API keys).\n",
						st.Path(), users, keys)
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "sweep",
			Short: "Delete expired dashboard sessions",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withStore(*dataDir, func(ctx context.Context, st *store.Store) error {
					n, err := st.SweepSessions(ctx)
					if err != nil {
						return err
					}
					outf(cmd.OutOrStdout(), "Removed %d expired session(s).\n", n)
					return nil
				})
			},
		},
	)
	return cmd
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// out / outf / outln are the CLI's write helpers. They swallow the write error
// on purpose: a failure writing to our own stdout (closed pipe, full disk) is
// not something a command can meaningfully report — there is nowhere left to
// report it to — and threading it back would obscure the real result.
func out(w io.Writer, args ...any)                 { _, _ = fmt.Fprint(w, args...) }
func outf(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }
func outln(w io.Writer, args ...any)               { _, _ = fmt.Fprintln(w, args...) }

func newTable(w io.Writer, headers ...string) *tabwriter.Writer {
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	outln(tw, strings.Join(headers, "\t"))
	return tw
}

func shortTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04")
}

func expiryText(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("2006-01-02 15:04")
}

// readPassword obtains a password without ever putting it on the command line.
//
// With --password-stdin it reads the whole of stdin (trailing newline trimmed),
// which is the `docker login` convention and what CI should use. Otherwise it
// prompts on the terminal with echo off, asking twice so a typo can't silently
// become the account's password. A non-TTY without --password-stdin is an error
// rather than a silent read from a pipe.
func readPassword(cmd *cobra.Command, fromStdin bool, prompt string, confirm bool) (string, error) {
	if fromStdin {
		b, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		pw := strings.TrimRight(string(b), "\r\n")
		if pw == "" {
			return "", errors.New("no password on stdin")
		}
		return pw, nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("not a terminal: pass the password with --password-stdin")
	}
	errOut := cmd.ErrOrStderr()

	out(errOut, prompt)
	first, err := term.ReadPassword(fd)
	outln(errOut)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if !confirm {
		return string(first), nil
	}

	out(errOut, "Confirm: ")
	second, err := term.ReadPassword(fd)
	outln(errOut)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("passwords do not match")
	}
	return string(first), nil
}

// parseExpiry accepts a Go duration ("720h", "90m") plus the day suffix people
// actually reach for ("30d"), which time.ParseDuration does not understand.
// Empty means "never expires".
func parseExpiry(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid --expires %q (want e.g. 30d, 12h, 90m)", s)
		}
		if days <= 0 {
			return 0, fmt.Errorf("invalid --expires %q (must be positive)", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --expires %q (want e.g. 30d, 12h, 90m)", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid --expires %q (must be positive)", s)
	}
	return d, nil
}

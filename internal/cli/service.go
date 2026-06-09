package cli

import (
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	appconfig "loose_ends/internal/config"
	"loose_ends/internal/server"
	"loose_ends/internal/storage"
	"loose_ends/internal/tailnet"
)

func newServeCommand(out io.Writer) *cobra.Command {
	var bind string
	var dbDSN string
	var port int
	var allowPublic bool
	var skipMigrate bool
	var tailnetMode string
	var installLaunchd bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the loopback (and optionally tailnet) HTTP service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if installLaunchd {
				return installLaunchdAgent(out)
			}

			cfg, _, err := appconfig.Load()
			if err != nil {
				return err
			}
			if dbDSN != "" {
				cfg.Service.Database.DSN = dbDSN
			}
			if tailnetMode != "" {
				switch strings.ToLower(tailnetMode) {
				case "on", "true":
					cfg.Service.TailnetEnabled = true
				case "off", "false":
					cfg.Service.TailnetEnabled = false
				default:
					return fmt.Errorf("--tailnet must be on or off")
				}
			}

			listenAddr, err := serviceListenAddr(cfg.Service.ListenLoopback, bind, port)
			if err != nil {
				return err
			}

			authToken := strings.TrimSpace(cfg.Service.AuthToken)
			host, portText, err := net.SplitHostPort(listenAddr)
			if err != nil {
				return fmt.Errorf("parse listen address: %w", err)
			}
			if !isLoopbackHost(host) && authToken == "" && !allowPublic {
				return fmt.Errorf("refusing to bind non-loopback address %q without authentication: "+
					"set service.auth_token (and send it as server.token from the CLI) or pass --allow-public to override", listenAddr)
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			if !skipMigrate {
				if err := storage.RunMigrations(cfg.Service.Database.DSN, ""); err != nil {
					return fmt.Errorf("apply migrations before serving: %w", err)
				}
			}

			repo, err := storage.Open(ctx, cfg.Service.Database.DSN)
			if err != nil {
				return err
			}
			defer repo.Close()

			serverCfg := server.Config{
				ListenAddr: listenAddr,
				Version:    server.DefaultVersion,
				Store:      repo,
				AuthToken:  authToken,
			}

			if cfg.Service.TailnetEnabled {
				if authToken == "" {
					_, _ = fmt.Fprintln(out, "warning: tailnet exposure is on with no service.auth_token — every device on your tailnet can reach the API unauthenticated (gated only by Tailscale ACLs). Set service.auth_token for defense-in-depth.")
				}
				authKey, err := tailnet.AuthKeyFromFile(tailnet.DefaultAuthKeyPath)
				if err != nil {
					return err
				}
				node, err := tailnet.Start(ctx, tailnet.Config{
					Hostname: cfg.Service.TailnetHostname,
					StateDir: cfg.Service.TailnetStateDir,
					AuthKey:  authKey,
					Port:     portText,
				})
				if err != nil {
					return fmt.Errorf("start tailnet listener: %w", err)
				}
				defer node.Close()
				serverCfg.TailnetListener = node.Listener()
				_, _ = fmt.Fprintf(out, "tailnet node %q serving on port %s\n", cfg.Service.TailnetHostname, portText)
			}

			_, _ = fmt.Fprintf(out, "serving on http://%s\n", listenAddr)
			return server.Run(ctx, serverCfg)
		},
	}

	cmd.Flags().StringVar(&bind, "bind", "", "loopback bind address")
	cmd.Flags().IntVar(&port, "port", 0, "loopback port")
	cmd.Flags().StringVar(&dbDSN, "db", "", "Postgres DSN")
	cmd.Flags().StringVar(&tailnetMode, "tailnet", "", "override tailnet exposure: on|off")
	cmd.Flags().BoolVar(&allowPublic, "allow-public", false, "allow binding a non-loopback address even without an auth token (unsafe)")
	cmd.Flags().BoolVar(&skipMigrate, "skip-migrate", false, "do not apply migrations on startup")
	cmd.Flags().BoolVar(&installLaunchd, "install-launchd", false, "write a launchd agent plist to ~/Library/LaunchAgents and exit (macOS)")
	return cmd
}

func newMigrateCommand(out io.Writer) *cobra.Command {
	var dbDSN string
	var migrationsDir string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := appconfig.Load()
			if err != nil {
				return err
			}
			if dbDSN == "" {
				dbDSN = cfg.Service.Database.DSN
			}

			if err := storage.RunMigrations(dbDSN, migrationsDir); err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, "migrations applied")
			return err
		},
	}

	cmd.Flags().StringVar(&dbDSN, "db", "", "Postgres DSN")
	cmd.Flags().StringVar(&migrationsDir, "path", "", "migrations directory (default: migrations embedded in the binary)")
	return cmd
}

func serviceListenAddr(configAddr string, bind string, port int) (string, error) {
	host, portText, err := net.SplitHostPort(configAddr)
	if err != nil {
		return "", fmt.Errorf("parse service.listen_loopback: %w", err)
	}
	if bind != "" {
		host = bind
	}
	if port != 0 {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("port must be between 1 and 65535")
		}
		portText = strconv.Itoa(port)
	}
	return net.JoinHostPort(host, portText), nil
}

// isLoopbackHost reports whether binding host keeps the service on the local
// machine only. An empty host (bind-all interfaces) is treated as non-loopback.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	switch host {
	case "":
		return false
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// installLaunchdAgent writes a launchd user-agent plist that runs `loose-ends
// serve` at login, then prints how to load it. macOS only.
func installLaunchdAgent(out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, "com.scshafe.loose-ends.plist")
	if err := os.WriteFile(path, []byte(renderLaunchdPlist(exe, home)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(out, "wrote %s\n", path)
	_, _ = fmt.Fprintf(out, "load it with:  launchctl load %s\n", path)
	_, _ = fmt.Fprintf(out, "unload it with: launchctl unload %s\n", path)
	return nil
}

func renderLaunchdPlist(exe, home string) string {
	logDir := filepath.Join(home, "Library", "Logs")
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.scshafe.loose-ends</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>%s/loose-ends.log</string>
  <key>StandardErrorPath</key>
  <string>%s/loose-ends.err.log</string>
</dict>
</plist>
`, xmlEscape(exe), xmlEscape(logDir), xmlEscape(logDir))
}

// xmlEscape escapes a string for safe inclusion inside a plist <string> element.
func xmlEscape(s string) string {
	var buf strings.Builder
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

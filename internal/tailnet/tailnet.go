// Package tailnet exposes the loose-ends HTTP service on the user's Tailscale
// network via tailscale.com/tsnet. The service joins the tailnet as its own node
// (MagicDNS name <hostname>.<tailnet>.ts.net); Tailscale provides identity and
// auth at the network layer, so nothing is reachable from the public internet
// (see LOOSE_ENDS_PLAN.md §13).
package tailnet

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"tailscale.com/tsnet"
)

// DefaultAuthKeyPath is where the service looks for a Tailscale auth key so the
// secret never has to be passed on the command line.
const DefaultAuthKeyPath = "~/.config/loose-ends/tsnet.authkey"

type Config struct {
	Hostname string // tailnet hostname (MagicDNS); default "loose-ends"
	StateDir string // where tsnet persists node state after first registration
	AuthKey  string // optional; tsnet also honours the TS_AUTHKEY env var
	Port     string // the TCP port to listen on, matching the loopback service
}

// Node is a running tsnet node with a tailnet listener.
type Node struct {
	srv *tsnet.Server
	ln  net.Listener
}

// Listener is the net.Listener bound on the tailnet.
func (n *Node) Listener() net.Listener {
	if n == nil {
		return nil
	}
	return n.ln
}

// Close tears down the tailnet node.
func (n *Node) Close() error {
	if n == nil || n.srv == nil {
		return nil
	}
	return n.srv.Close()
}

// Start brings up a tsnet node and returns a TCP listener on the tailnet for the
// configured port. It authenticates with cfg.AuthKey (or TS_AUTHKEY), or reuses
// previously-persisted state in StateDir. The caller serves the same HTTP
// handler on the returned listener as it does on loopback.
func Start(ctx context.Context, cfg Config) (*Node, error) {
	dir, err := expandPath(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	hostname := strings.TrimSpace(cfg.Hostname)
	if hostname == "" {
		hostname = "loose-ends"
	}
	port := strings.TrimSpace(cfg.Port)
	if port == "" {
		return nil, fmt.Errorf("tailnet: service port is required")
	}

	srv := &tsnet.Server{
		Hostname: hostname,
		Dir:      dir,
		AuthKey:  cfg.AuthKey,
	}
	if _, err := srv.Up(ctx); err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("bring up tailnet node %q: %w", hostname, err)
	}
	ln, err := srv.Listen("tcp", ":"+port)
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("listen on tailnet port %s: %w", port, err)
	}
	return &Node{srv: srv, ln: ln}, nil
}

// AuthKeyFromFile reads a Tailscale auth key from path. A missing file yields an
// empty key (not an error) so an already-registered node can start from
// persisted state with no key present.
func AuthKeyFromFile(path string) (string, error) {
	expanded, err := expandPath(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(expanded)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read tailnet auth key %s: %w", expanded, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

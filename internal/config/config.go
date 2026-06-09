package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

const EnvConfigPath = "LOOSE_ENDS_CONFIG"

type Config struct {
	Server   ServerConfig   `toml:"server" json:"server"`
	Display  DisplayConfig  `toml:"display" json:"display"`
	Defaults DefaultsConfig `toml:"defaults" json:"defaults"`
	Service  ServiceConfig  `toml:"service" json:"service"`
}

type ServerConfig struct {
	URL   string `toml:"url" json:"url"`
	Token string `toml:"token,omitempty" json:"token,omitempty"`
}

type DisplayConfig struct {
	Color      string `toml:"color" json:"color"`
	TreeIndent string `toml:"tree_indent" json:"tree_indent"`
	IDLength   int    `toml:"id_length" json:"id_length"`
}

type DefaultsConfig struct {
	Tags []string `toml:"tags,omitempty" json:"tags,omitempty"`
}

type ServiceConfig struct {
	ListenLoopback  string                `toml:"listen_loopback" json:"listen_loopback"`
	AuthToken       string                `toml:"auth_token,omitempty" json:"auth_token,omitempty"`
	TailnetEnabled  bool                  `toml:"tailnet_enabled" json:"tailnet_enabled"`
	TailnetHostname string                `toml:"tailnet_hostname" json:"tailnet_hostname"`
	TailnetStateDir string                `toml:"tailnet_state_dir" json:"tailnet_state_dir"`
	Database        ServiceDatabaseConfig `toml:"database" json:"database"`
}

type ServiceDatabaseConfig struct {
	DSN string `toml:"dsn" json:"dsn"`
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			URL: "http://127.0.0.1:17890",
		},
		Display: DisplayConfig{
			Color:      "auto",
			TreeIndent: "  ",
			IDLength:   8,
		},
		Defaults: DefaultsConfig{},
		Service: ServiceConfig{
			ListenLoopback:  "127.0.0.1:17890",
			TailnetEnabled:  true,
			TailnetHostname: "loose-ends",
			TailnetStateDir: "~/.config/loose-ends/tsnet-state",
			Database: ServiceDatabaseConfig{
				DSN: "postgres://loose_ends@127.0.0.1:5432/loose_ends?sslmode=disable",
			},
		},
	}
}

func Path() (string, error) {
	if override := os.Getenv(EnvConfigPath); override != "" {
		return override, nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(configDir, "loose-ends", "config.toml"), nil
}

func Load() (*Config, string, error) {
	path, err := Path()
	if err != nil {
		return nil, "", err
	}

	cfg, err := LoadFile(path)
	if err != nil {
		return nil, "", err
	}
	return cfg, path, nil
}

func LoadFile(path string) (*Config, error) {
	cfg := Default()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := Save(path, cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat config %s: %w", path, err)
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return cfg, nil
}

func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open config %s: %w", path, err)
	}
	defer file.Close()

	if err := toml.NewEncoder(file).Encode(cfg); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func Marshal(cfg *Config) (string, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return "", fmt.Errorf("encode config: %w", err)
	}
	return buf.String(), nil
}

func (c *Config) Set(key string, value string) error {
	switch key {
	case "server.url":
		c.Server.URL = value
	case "server.token":
		c.Server.Token = value
	case "display.color":
		if value != "auto" && value != "always" && value != "never" {
			return fmt.Errorf("display.color must be one of: auto, always, never")
		}
		c.Display.Color = value
	case "display.tree_indent":
		c.Display.TreeIndent = value
	case "display.id_length":
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return fmt.Errorf("display.id_length must be a positive integer")
		}
		c.Display.IDLength = parsed
	case "defaults.tags":
		c.Defaults.Tags = splitList(value)
	case "service.listen_loopback":
		c.Service.ListenLoopback = value
	case "service.auth_token":
		c.Service.AuthToken = value
	case "service.tailnet_enabled":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("service.tailnet_enabled must be a boolean")
		}
		c.Service.TailnetEnabled = parsed
	case "service.tailnet_hostname":
		c.Service.TailnetHostname = value
	case "service.tailnet_state_dir":
		c.Service.TailnetStateDir = value
	case "service.database.dsn":
		c.Service.Database.DSN = value
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

func splitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

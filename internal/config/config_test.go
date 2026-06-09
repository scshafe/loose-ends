package config

import (
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Server.URL != "http://127.0.0.1:17890" {
		t.Errorf("server.url = %q", cfg.Server.URL)
	}
	if cfg.Display.Color != "auto" {
		t.Errorf("display.color = %q, want auto", cfg.Display.Color)
	}
	if cfg.Display.IDLength != 8 {
		t.Errorf("display.id_length = %d, want 8", cfg.Display.IDLength)
	}
	if !cfg.Service.TailnetEnabled {
		t.Errorf("service.tailnet_enabled = false, want true")
	}
	if cfg.Service.Database.DSN == "" {
		t.Errorf("service.database.dsn is empty")
	}
	if cfg.Service.AuthToken != "" {
		t.Errorf("service.auth_token = %q, want empty by default", cfg.Service.AuthToken)
	}
}

func TestSet(t *testing.T) {
	cfg := Default()
	good := []struct {
		key   string
		val   string
		check func(*Config) bool
	}{
		{"server.url", "http://x:1", func(c *Config) bool { return c.Server.URL == "http://x:1" }},
		{"server.token", "tok", func(c *Config) bool { return c.Server.Token == "tok" }},
		{"service.auth_token", "atk", func(c *Config) bool { return c.Service.AuthToken == "atk" }},
		{"display.color", "never", func(c *Config) bool { return c.Display.Color == "never" }},
		{"display.id_length", "12", func(c *Config) bool { return c.Display.IDLength == 12 }},
		{"service.listen_loopback", "127.0.0.1:9", func(c *Config) bool { return c.Service.ListenLoopback == "127.0.0.1:9" }},
		{"service.database.dsn", "postgres://x", func(c *Config) bool { return c.Service.Database.DSN == "postgres://x" }},
		{"defaults.tags", "a, b ,c", func(c *Config) bool { return len(c.Defaults.Tags) == 3 && c.Defaults.Tags[2] == "c" }},
	}
	for _, tc := range good {
		if err := cfg.Set(tc.key, tc.val); err != nil {
			t.Fatalf("Set(%q,%q): %v", tc.key, tc.val, err)
		}
		if !tc.check(cfg) {
			t.Errorf("Set(%q,%q) did not apply", tc.key, tc.val)
		}
	}

	bad := []struct{ key, val string }{
		{"nope.key", "x"},
		{"display.color", "rainbow"},
		{"display.id_length", "-1"},
		{"display.id_length", "abc"},
		{"service.tailnet_enabled", "maybe"},
	}
	for _, tc := range bad {
		if err := cfg.Set(tc.key, tc.val); err == nil {
			t.Errorf("Set(%q,%q) expected error, got nil", tc.key, tc.val)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	cfg.Service.AuthToken = "rt-token"
	cfg.Display.Color = "never"
	cfg.Service.ListenLoopback = "127.0.0.1:12345"

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if loaded.Service.AuthToken != "rt-token" {
		t.Errorf("auth_token not persisted: %q", loaded.Service.AuthToken)
	}
	if loaded.Display.Color != "never" {
		t.Errorf("color not persisted: %q", loaded.Display.Color)
	}
	if loaded.Service.ListenLoopback != "127.0.0.1:12345" {
		t.Errorf("listen_loopback not persisted: %q", loaded.Service.ListenLoopback)
	}
}

func TestLoadFileCreatesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile on missing path: %v", err)
	}
	if cfg.Display.Color != "auto" {
		t.Errorf("expected default color auto, got %q", cfg.Display.Color)
	}
}

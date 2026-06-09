package cli

import "testing"

func TestServiceListenAddr(t *testing.T) {
	cases := []struct {
		name    string
		cfg     string
		bind    string
		port    int
		want    string
		wantErr bool
	}{
		{"default", "127.0.0.1:17890", "", 0, "127.0.0.1:17890", false},
		{"override bind", "127.0.0.1:17890", "0.0.0.0", 0, "0.0.0.0:17890", false},
		{"override port", "127.0.0.1:17890", "", 18000, "127.0.0.1:18000", false},
		{"override both", "127.0.0.1:17890", "::1", 22, "[::1]:22", false},
		{"bad config addr", "not-an-addr", "", 0, "", true},
		{"port too high", "127.0.0.1:17890", "", 70000, "", true},
		{"port negative", "127.0.0.1:17890", "", -1, "", true},
	}
	for _, tc := range cases {
		got, err := serviceListenAddr(tc.cfg, tc.bind, tc.port)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	loopback := []string{"127.0.0.1", "::1", "localhost", "127.0.0.5"}
	for _, h := range loopback {
		if !isLoopbackHost(h) {
			t.Errorf("expected %q to be loopback", h)
		}
	}
	public := []string{"", "0.0.0.0", "192.168.1.5", "10.0.0.1", "example.com", "::"}
	for _, h := range public {
		if isLoopbackHost(h) {
			t.Errorf("expected %q to be non-loopback", h)
		}
	}
}

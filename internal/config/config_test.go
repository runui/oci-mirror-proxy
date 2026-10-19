package config

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogLevel(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  slog.Level
	}{{"", slog.LevelInfo}, {"debug", slog.LevelDebug}, {"INFO", slog.LevelInfo}, {"warn", slog.LevelWarn}, {"error", slog.LevelError}} {
		c := Default()
		c.LogLevel = tc.value
		got, err := c.LoggingLevel()
		if err != nil || got != tc.want || c.Validate() != nil {
			t.Fatalf("log level %q: got %v, %v", tc.value, got, err)
		}
	}
	c := Default()
	c.LogLevel = "verbose"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "log_level") {
		t.Fatalf("invalid level: %v", err)
	}
	data, err := Encode(Default())
	if err != nil || strings.Contains(string(data), "log_level") {
		t.Fatalf("default configuration must omit log_level: %s, %v", data, err)
	}
	c, err = Decode([]byte("version: 1\nlog_level: debug\n"))
	if err != nil || c.LogLevel != "debug" {
		t.Fatalf("decode log_level: %+v, %v", c, err)
	}
}

func validUpstream(id string) Upstream {
	return Upstream{
		ID:       id,
		URL:      "https://mirror.example.com",
		Timeouts: Timeouts{FirstByte: time.Second, Overall: time.Minute},
		Auth:     Auth{Type: AuthNone},
	}
}

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("Default() is invalid: %v", err)
	}
	if c.Listen != DefaultListen {
		t.Errorf("listen = %q, want %q", c.Listen, DefaultListen)
	}
	reg, ok := c.Registries["docker.io"]
	if !ok {
		t.Fatal("default config must contain docker.io")
	}
	if reg.Origin != "https://registry-1.docker.io" {
		t.Errorf("docker.io origin = %q", reg.Origin)
	}
	if reg.Race.Enabled {
		t.Error("racing must be disabled by default")
	}
}

func TestApplyDefaultsFillsUpstreams(t *testing.T) {
	c := Config{Registries: map[string]Registry{
		"ghcr.io": {Upstreams: []Upstream{{ID: "a", URL: "https://a.example.com"}}},
	}}
	c.ApplyDefaults()
	reg := c.Registries["ghcr.io"]
	if reg.Origin != "https://ghcr.io" {
		t.Errorf("origin = %q, want https://ghcr.io", reg.Origin)
	}
	u := reg.Upstreams[0]
	if u.Timeouts.Connect != defaultConnect {
		t.Errorf("connect timeout = %v, want default %v", u.Timeouts.Connect, defaultConnect)
	}
	if u.Timeouts.FirstByte != defaultFirstByte || u.Timeouts.Overall != defaultOverall {
		t.Errorf("timeouts not defaulted: %+v", u.Timeouts)
	}
	if u.Auth.Type != AuthNone {
		t.Errorf("auth type = %q, want none", u.Auth.Type)
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	c := Default()
	c.Version = 9
	c.Listen = "no-port"
	reg := c.Registries["docker.io"]
	reg.Race = Race{Enabled: true, Delay: 0, MaxParallel: 1}
	reg.Upstreams = []Upstream{
		{ID: "dup", URL: "ftp://bad", Timeouts: Timeouts{FirstByte: time.Second, Overall: time.Second}, Auth: Auth{Type: AuthNone}},
		{ID: "dup", URL: "https://ok.example.com", Timeouts: Timeouts{FirstByte: time.Second, Overall: time.Second}, Auth: Auth{Type: AuthNone}},
		{ID: "bad id!", URL: "https://x.example.com", Timeouts: Timeouts{FirstByte: time.Second, Overall: time.Second}, Auth: Auth{Type: AuthBasic}},
	}
	c.Registries["docker.io"] = reg

	err := c.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	msg := err.Error()
	for _, want := range []string{
		"unsupported config version",
		`listen "no-port"`,
		"race.delay must be positive",
		"race.max_parallel must be at least 2",
		`duplicate upstream id "dup"`,
		"must use http or https",
		`id "bad id!"`,
		"auth type basic requires username and password",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("validation error missing %q\nfull error:\n%s", want, msg)
		}
	}
}

func TestAuthRules(t *testing.T) {
	tests := []struct {
		name string
		auth Auth
		ok   bool
	}{
		{"none", Auth{Type: AuthNone}, true},
		{"none with credentials", Auth{Type: AuthNone, Username: "u"}, false},
		{"basic ok", Auth{Type: AuthBasic, Username: "u", Password: "env:PW"}, true},
		{"basic literal password", Auth{Type: AuthBasic, Username: "u", Password: "literal"}, false},
		{"token anonymous", Auth{Type: AuthToken}, true},
		{"token with user and ref", Auth{Type: AuthToken, Username: "u", Password: "file:/x"}, true},
		{"token user without password", Auth{Type: AuthToken, Username: "u"}, false},
		{"unknown type", Auth{Type: "oauth"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAuth(tc.auth)
			if (err == nil) != tc.ok {
				t.Fatalf("validateAuth(%+v) = %v, want ok=%v", tc.auth, err, tc.ok)
			}
		})
	}
}

func TestProxyURL(t *testing.T) {
	tests := []struct {
		name string
		c    Config
		want string
	}{
		{"loopback", Config{Listen: "127.0.0.1:24817"}, "http://127.0.0.1:24817"},
		{"wildcard maps to loopback", Config{Listen: "0.0.0.0:24817"}, "http://127.0.0.1:24817"},
		{"ipv6 wildcard", Config{Listen: "[::]:24817"}, "http://127.0.0.1:24817"},
		{"advertise wins", Config{Listen: "0.0.0.0:24817", Advertise: "192.168.1.10:24817"}, "http://192.168.1.10:24817"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.ProxyURL(); got != tc.want {
				t.Fatalf("ProxyURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAdvertiseMustHaveHost(t *testing.T) {
	c := Default()
	c.Advertise = ":24817"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "must include a concrete host") {
		t.Fatalf("advertise without host: err = %v", err)
	}
}

func TestCloneIsDeep(t *testing.T) {
	c := Default()
	reg := c.Registries["docker.io"]
	reg.Upstreams = []Upstream{validUpstream("a")}
	c.Registries["docker.io"] = reg

	cp := c.Clone()
	cp.Registries["docker.io"].Upstreams[0].URL = "https://changed.example.com"
	cp.Registries["ghcr.io"] = Registry{}

	if c.Registries["docker.io"].Upstreams[0].URL == "https://changed.example.com" {
		t.Error("clone shares upstream slice with original")
	}
	if _, ok := c.Registries["ghcr.io"]; ok {
		t.Error("clone shares registry map with original")
	}
}

func TestDecodeRejectsUnknownKeys(t *testing.T) {
	_, err := Decode([]byte("version: 1\nlisten: 127.0.0.1:24817\nlisen: typo\n"))
	if err == nil {
		t.Fatal("unknown key must be rejected")
	}
}

func TestDecodeEmptyFile(t *testing.T) {
	if _, err := Decode([]byte("")); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty file error = %v", err)
	}
}

func TestDockerConfigIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "examples", "config.docker.yaml"))
	if err != nil {
		t.Fatalf("Docker config is invalid: %v", err)
	}
	if cfg.Listen != DefaultListen || cfg.ProxyURL() != "http://"+DefaultListen {
		t.Fatalf("Docker config must remain loopback-only: %+v", cfg)
	}
	if cfg.Admin.Socket != "/run/oci-mirror-proxy/admin.sock" {
		t.Fatalf("unexpected Docker admin socket: %q", cfg.Admin.Socket)
	}
}

// TestExampleConfigIsValid keeps the documented example in step with the schema.
func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "examples", "config.example.yaml"))
	if err != nil {
		t.Fatalf("example config is invalid: %v", err)
	}
	if got := len(cfg.Registries["docker.io"].Upstreams); got != 3 {
		t.Fatalf("example upstreams = %d, want 3", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	c := Default()
	reg := c.Registries["docker.io"]
	reg.Race = Race{Enabled: true, Delay: 250 * time.Millisecond, MaxParallel: 3}
	reg.Upstreams = []Upstream{{
		ID:       "mirror-a",
		URL:      "https://mirror-a.example.com/hub",
		Timeouts: Timeouts{FirstByte: 2 * time.Second, Overall: 30 * time.Second},
		Auth:     Auth{Type: AuthBasic, Username: "alice", Password: "env:MIRROR_A"},
		TLS:      TLS{CAFile: "/etc/ssl/ca.pem"},
	}}
	c.Registries["docker.io"] = reg

	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Registries["docker.io"].Race != reg.Race {
		t.Errorf("race round trip = %+v, want %+v", got.Registries["docker.io"].Race, reg.Race)
	}
	if got.Registries["docker.io"].Upstreams[0] != reg.Upstreams[0] {
		t.Errorf("upstream round trip = %+v, want %+v", got.Registries["docker.io"].Upstreams[0], reg.Upstreams[0])
	}
}

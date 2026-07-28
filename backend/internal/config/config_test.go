package config

import (
	"strings"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	valid := func() *Config {
		return &Config{
			App: AppConfig{Env: "production"},
			Server: ServerConfig{
				Port:           8080,
				AllowedOrigins: []string{"https://app.phonara.example"},
			},
			JWT: JWTConfig{
				AccessSecret:  strings.Repeat("a", 32),
				RefreshSecret: strings.Repeat("b", 32),
				AccessTTL:     15 * time.Minute,
				RefreshTTL:    7 * 24 * time.Hour,
			},
			Engine: EngineConfig{Timeout: 2 * time.Minute},
		}
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "short access secret",
			mutate: func(cfg *Config) {
				cfg.JWT.AccessSecret = "short"
			},
		},
		{
			name: "shared signing secret",
			mutate: func(cfg *Config) {
				cfg.JWT.RefreshSecret = cfg.JWT.AccessSecret
			},
		},
		{
			name: "wildcard production cors",
			mutate: func(cfg *Config) {
				cfg.Server.AllowedOrigins = []string{"*"}
			},
		},
		{
			name: "invalid port",
			mutate: func(cfg *Config) {
				cfg.Server.Port = 70000
			},
		},
		{
			name: "non-positive pronunciation engine timeout",
			mutate: func(cfg *Config) {
				cfg.Engine.Timeout = 0
			},
		},
	}

	if err := valid().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid()
			tt.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSplitCSV(t *testing.T) {
	t.Parallel()
	got := splitCSV(" https://a.example,https://b.example ,, ")
	if len(got) != 2 || got[0] != "https://a.example" || got[1] != "https://b.example" {
		t.Fatalf("splitCSV returned %#v", got)
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "Go duration", value: "15m", want: 15 * time.Minute},
		{name: "whole days", value: "7d", want: 7 * 24 * time.Hour},
		{name: "invalid unit", value: "7days", wantErr: true},
		{name: "zero days", value: "0d", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseDuration(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDuration(%q) unexpectedly succeeded", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDuration(%q): %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("parseDuration(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

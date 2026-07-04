package main

import (
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestParseCLIArgsActions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantAction cliAction
		wantErr    bool
	}{
		{name: "default run", args: nil, wantAction: cliActionRun},
		{name: "help", args: []string{"--help"}, wantAction: cliActionHelp},
		{name: "short help", args: []string{"-h"}, wantAction: cliActionHelp},
		{name: "version", args: []string{"--version"}, wantAction: cliActionVersion},
		{name: "short version", args: []string{"-v"}, wantAction: cliActionVersion},
		{name: "version command", args: []string{"version"}, wantAction: cliActionVersion},
		{name: "unknown flag", args: []string{"--unknown"}, wantErr: true},
		{name: "extra positional", args: []string{"extra"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCLIArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCLIArgs() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCLIArgs() error = %v", err)
			}
			if got.Action != tt.wantAction {
				t.Fatalf("parseCLIArgs() action = %v, want %v", got.Action, tt.wantAction)
			}
		})
	}
}

func TestParseCLIArgsRuntimeOptions(t *testing.T) {
	got, err := parseCLIArgs([]string{
		"--config", "/tmp/ccx/config.json",
		"--statedir", "/tmp/ccx-state",
		"--logdir", "/tmp/ccx-logs",
	})
	if err != nil {
		t.Fatalf("parseCLIArgs() error = %v", err)
	}
	if got.ConfigPath != "/tmp/ccx/config.json" {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, "/tmp/ccx/config.json")
	}
	if got.StateDir != "/tmp/ccx-state" {
		t.Fatalf("StateDir = %q, want %q", got.StateDir, "/tmp/ccx-state")
	}
	if got.LogDir != "/tmp/ccx-logs" {
		t.Fatalf("LogDir = %q, want %q", got.LogDir, "/tmp/ccx-logs")
	}
}

func TestResolveRuntimePathsDefaults(t *testing.T) {
	got, err := resolveRuntimePaths(cliOptions{}, &config.EnvConfig{LogDir: "logs"})
	if err != nil {
		t.Fatalf("resolveRuntimePaths() error = %v", err)
	}

	want := runtimePaths{
		ConfigPath:                 ".config/config.json",
		StateDir:                   ".config",
		MetricsDBPath:              filepath.Join(".config", "metrics.db"),
		ThinkingCacheDBPath:        filepath.Join(".config", "thinking_cache.db"),
		ConversationStatePath:      filepath.Join(".config", "conversation_state.json"),
		ScheduledRecoveryStatePath: filepath.Join(".config", "scheduled_recovery_state.json"),
		LogDir:                     "logs",
		BackupDir:                  filepath.Join(".config", "backups"),
	}
	if got != want {
		t.Fatalf("resolveRuntimePaths() = %#v, want %#v", got, want)
	}
}

func TestResolveRuntimePathsConfigPathDoesNotMoveState(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "profiles", "custom.json")
	got, err := resolveRuntimePaths(cliOptions{ConfigPath: configPath}, &config.EnvConfig{LogDir: "logs"})
	if err != nil {
		t.Fatalf("resolveRuntimePaths() error = %v", err)
	}

	if got.ConfigPath != configPath {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, configPath)
	}
	if got.MetricsDBPath != filepath.Join(".config", "metrics.db") {
		t.Fatalf("MetricsDBPath = %q", got.MetricsDBPath)
	}
	if got.ThinkingCacheDBPath != filepath.Join(".config", "thinking_cache.db") {
		t.Fatalf("ThinkingCacheDBPath = %q", got.ThinkingCacheDBPath)
	}
	if got.ConversationStatePath != filepath.Join(".config", "conversation_state.json") {
		t.Fatalf("ConversationStatePath = %q", got.ConversationStatePath)
	}
	if got.ScheduledRecoveryStatePath != filepath.Join(".config", "scheduled_recovery_state.json") {
		t.Fatalf("ScheduledRecoveryStatePath = %q", got.ScheduledRecoveryStatePath)
	}
	if got.LogDir != "logs" {
		t.Fatalf("LogDir = %q, want logs", got.LogDir)
	}
}

func TestResolveRuntimePathsStateDir(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	got, err := resolveRuntimePaths(cliOptions{StateDir: stateDir}, &config.EnvConfig{LogDir: "logs"})
	if err != nil {
		t.Fatalf("resolveRuntimePaths() error = %v", err)
	}
	if got.StateDir != stateDir {
		t.Fatalf("StateDir = %q, want %q", got.StateDir, stateDir)
	}
	if got.MetricsDBPath != filepath.Join(stateDir, "metrics.db") {
		t.Fatalf("MetricsDBPath = %q", got.MetricsDBPath)
	}
	if got.ThinkingCacheDBPath != filepath.Join(stateDir, "thinking_cache.db") {
		t.Fatalf("ThinkingCacheDBPath = %q", got.ThinkingCacheDBPath)
	}
	if got.ConversationStatePath != filepath.Join(stateDir, "conversation_state.json") {
		t.Fatalf("ConversationStatePath = %q", got.ConversationStatePath)
	}
	if got.ScheduledRecoveryStatePath != filepath.Join(stateDir, "scheduled_recovery_state.json") {
		t.Fatalf("ScheduledRecoveryStatePath = %q", got.ScheduledRecoveryStatePath)
	}
}

func TestResolveRuntimePathsLogDirPrecedence(t *testing.T) {
	got, err := resolveRuntimePaths(cliOptions{LogDir: "/tmp/cli-logs"}, &config.EnvConfig{LogDir: "/tmp/env-logs"})
	if err != nil {
		t.Fatalf("resolveRuntimePaths() error = %v", err)
	}
	if got.LogDir != filepath.Clean("/tmp/cli-logs") {
		t.Fatalf("LogDir = %q, want CLI logdir", got.LogDir)
	}

	got, err = resolveRuntimePaths(cliOptions{}, &config.EnvConfig{LogDir: "/tmp/env-logs"})
	if err != nil {
		t.Fatalf("resolveRuntimePaths() error = %v", err)
	}
	if got.LogDir != "/tmp/env-logs" {
		t.Fatalf("LogDir = %q, want env logdir", got.LogDir)
	}
}

func TestResolveRuntimePathsExpandsHome(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	got, err := resolveRuntimePaths(cliOptions{
		ConfigPath: "~/ccx/config.json",
		StateDir:   "~/state/ccx/runtime",
		LogDir:     "~/state/ccx/logs",
	}, &config.EnvConfig{LogDir: "logs"})
	if err != nil {
		t.Fatalf("resolveRuntimePaths() error = %v", err)
	}

	wantConfig := filepath.Join(homeDir, "ccx", "config.json")
	wantStateDir := filepath.Join(homeDir, "state", "ccx", "runtime")
	wantLogDir := filepath.Join(homeDir, "state", "ccx", "logs")
	if got.ConfigPath != wantConfig {
		t.Fatalf("ConfigPath = %q, want %q", got.ConfigPath, wantConfig)
	}
	if got.StateDir != wantStateDir {
		t.Fatalf("StateDir = %q, want %q", got.StateDir, wantStateDir)
	}
	if got.LogDir != wantLogDir {
		t.Fatalf("LogDir = %q, want %q", got.LogDir, wantLogDir)
	}
}

func TestResolveRuntimePathsRejectsOtherUserHome(t *testing.T) {
	if _, err := resolveRuntimePaths(cliOptions{ConfigPath: "~other/ccx.json"}, &config.EnvConfig{LogDir: "logs"}); err == nil {
		t.Fatalf("resolveRuntimePaths() error = nil, want error")
	}
	if _, err := resolveRuntimePaths(cliOptions{StateDir: "~other/state"}, &config.EnvConfig{LogDir: "logs"}); err == nil {
		t.Fatalf("resolveRuntimePaths() error = nil, want error")
	}
	if _, err := resolveRuntimePaths(cliOptions{LogDir: "~other/logs"}, &config.EnvConfig{LogDir: "logs"}); err == nil {
		t.Fatalf("resolveRuntimePaths() error = nil, want error")
	}
}

func TestResolveRuntimePathsLogDirNone(t *testing.T) {
	tests := []struct {
		name    string
		logDir  string
		wantDir string
	}{
		{"--logdir none", "none", "none"},
		{"--logdir null", "null", "none"},
		{"--logdir NONE", "NONE", "none"},
		{"--logdir NULL", "NULL", "none"},
		{"--logdir None", "None", "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRuntimePaths(cliOptions{LogDir: tt.logDir}, &config.EnvConfig{LogDir: "logs"})
			if err != nil {
				t.Fatalf("resolveRuntimePaths() error = %v", err)
			}
			if got.LogDir != tt.wantDir {
				t.Fatalf("LogDir = %q, want %q", got.LogDir, tt.wantDir)
			}
		})
	}
}

func TestResolveRuntimePathsLogDirNoneFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		wantDir string
	}{
		{"LOG_DIR=none", "none", "none"},
		{"LOG_DIR=null", "null", "none"},
		{"LOG_DIR=NONE", "NONE", "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRuntimePaths(cliOptions{}, &config.EnvConfig{LogDir: tt.envVal})
			if err != nil {
				t.Fatalf("resolveRuntimePaths() error = %v", err)
			}
			if got.LogDir != tt.wantDir {
				t.Fatalf("LogDir = %q, want %q", got.LogDir, tt.wantDir)
			}
		})
	}
}

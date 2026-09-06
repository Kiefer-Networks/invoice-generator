package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTestScriptsStopOnGitFailure(t *testing.T) {
	bash, _ := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe")
	}
	powershell, _ := exec.LookPath("pwsh")
	if powershell == "" {
		powershell, _ = exec.LookPath("powershell")
	}
	for _, tc := range []struct {
		name, executable string
		args             []string
	}{
		{"test.sh", bash, nil},
		{"test.ps1", powershell, []string{"-NoProfile", "-File"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.executable == "" {
				t.Skip("runner shell is not installed on this host")
			}
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			for _, command := range []string{"git", "gofmt", "go"} {
				body := "echo later-stage >> \"$RUNNER_SENTINEL\"\nexit 0\n"
				batch := "@echo off\r\necho later-stage >> \"%RUNNER_SENTINEL%\"\r\nexit /b 0\r\n"
				if command == "git" {
					body = "echo injected-git-failure >&2\nexit 42\n"
					batch = "@echo off\r\necho injected-git-failure >&2\r\nexit /b 42\r\n"
				}
				if err := os.WriteFile(filepath.Join(bin, command), []byte("#!/usr/bin/env bash\n"+body), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(bin, command+".cmd"), []byte(batch), 0600); err != nil {
					t.Fatal(err)
				}
			}
			script, err := filepath.Abs(filepath.Join("..", "..", "scripts", tc.name))
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(root, "later-stage")
			cmd := exec.Command(tc.executable, append(tc.args, filepath.ToSlash(script))...)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "RUNNER_SENTINEL="+marker)
			if tc.name == "test.sh" && runtime.GOOS == "windows" {
				// Git Bash prepends its own tools to PATH during startup.
				envFile := filepath.Join(root, "bash-env")
				if err := os.WriteFile(envFile, []byte("export PATH=\"$(cygpath -u \"$RUNNER_BIN\"):$PATH\"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				cmd.Env = append(cmd.Env, "BASH_ENV="+filepath.ToSlash(envFile), "RUNNER_BIN="+bin)
			}
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Errorf("runner succeeded despite Git enumeration failure: %s", output)
			}
			if !strings.Contains(string(output), "injected-git-failure") || !strings.Contains(string(output), "Format") {
				t.Fatalf("injection did not reach the Format stage: %s", output)
			}
			for _, stage := range []string{"Vet", "Unit", "Integration", "Browser"} {
				if strings.Contains(string(output), stage) {
					t.Errorf("runner continued to %s: %s", stage, output)
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Errorf("gofmt or Go ran after Git failed: %v", err)
			}
		})
	}
}

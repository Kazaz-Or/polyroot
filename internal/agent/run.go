package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// NewRunDir returns a fresh, not-yet-created run directory under cacheDir.
func NewRunDir(cacheDir string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return filepath.Join(cacheDir, "run", hex.EncodeToString(b))
}

// CleanStaleRuns removes run directories older than maxAge, left behind if
// Polyroot was killed before it could clean up.
func CleanStaleRuns(cacheDir string, maxAge time.Duration) {
	root := filepath.Join(cacheDir, "run")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && e.IsDir() && time.Since(info.ModTime()) > maxAge {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// Run writes the spec's generated files, starts the agent as a child process
// (argv only, no shell), forwards termination signals, removes the run
// directory afterwards and returns the agent's exit code.
func Run(spec *LaunchSpec) (int, error) {
	path, err := exec.LookPath(spec.Command)
	if err != nil {
		return 1, fmt.Errorf("%s is not installed or not in PATH (see `polyroot agents`)", spec.Command)
	}
	if spec.RunDir != "" {
		defer os.RemoveAll(spec.RunDir)
	}
	for _, f := range spec.Files {
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
			return 1, err
		}
		if err := os.WriteFile(f.Path, f.Content, 0o600); err != nil {
			return 1, err
		}
	}

	cmd := exec.Command(path, spec.Args...)
	cmd.Args[0] = spec.Command
	cmd.Dir = spec.Dir
	cmd.Env = os.Environ()
	for _, e := range spec.Env {
		cmd.Env = append(cmd.Env, e.Key+"="+e.Value) // later entries win
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// The terminal already delivers Ctrl-C to the agent (same process group);
	// Polyroot must survive it to clean up. Other signals are forwarded.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return 1, err
	}
	go func() {
		for s := range sigs {
			if s == syscall.SIGTERM || s == syscall.SIGHUP {
				_ = cmd.Process.Signal(s)
			}
		}
	}()
	err = cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

var safeShellWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// Quote quotes s for display in a POSIX shell. Display only: Polyroot never
// executes quoted strings.
func Quote(s string) string {
	if s != "" && safeShellWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Print writes a human-readable description of spec to w.
func Print(w io.Writer, spec *LaunchSpec, resolvedPath string) {
	fmt.Fprintf(w, "agent:\n  %s", spec.Agent)
	if resolvedPath != "" {
		fmt.Fprintf(w, " (%s)", resolvedPath)
	}
	fmt.Fprintf(w, "\n\ncwd:\n  %s\n", spec.Dir)
	if len(spec.Env) > 0 {
		fmt.Fprintln(w, "\nenvironment:")
		for _, e := range spec.Env {
			fmt.Fprintf(w, "  %s=%s\n", e.Key, Quote(e.Value))
		}
	}
	if len(spec.Files) > 0 {
		fmt.Fprintln(w, "\ngenerated:")
		for _, f := range spec.Files {
			fmt.Fprintf(w, "  %s\n", f.Path)
		}
	}
	fmt.Fprintf(w, "\ncommand:\n  %s", Quote(spec.Command))
	for i := 0; i < len(spec.Args); i++ {
		line := Quote(spec.Args[i])
		// Keep "--flag value" pairs on one line.
		if strings.HasPrefix(spec.Args[i], "-") && !strings.Contains(spec.Args[i], "=") &&
			i+1 < len(spec.Args) && !strings.HasPrefix(spec.Args[i+1], "-") {
			i++
			line += " " + Quote(spec.Args[i])
		}
		fmt.Fprintf(w, " \\\n    %s", line)
	}
	fmt.Fprintln(w)
	if len(spec.Warnings) > 0 {
		fmt.Fprintln(w, "\nwarnings:")
		for _, m := range spec.Warnings {
			fmt.Fprintf(w, "  %s\n", m)
		}
	}
}

package main

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/wyattjs/chorus/internal/capture"
	"github.com/wyattjs/chorus/internal/output"
)

// quietLogs routes Go log output and render-sidecar stderr to a log file instead
// of the terminal, so the interactive TUI stays clean. It returns the log path
// (empty if it fell back to discarding) and a closer to call on shutdown.
func quietLogs() (path string, closeFn func()) {
	closeFn = func() {}
	var w io.Writer = io.Discard

	dir := logDir()
	if err := os.MkdirAll(dir, 0o755); err == nil {
		p := filepath.Join(dir, "chorus.log")
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			w, path, closeFn = f, p, func() { _ = f.Close() }
		}
	}

	log.SetOutput(w)
	capture.LogWriter = w
	output.LogWriter = w
	return path, closeFn
}

// logDir is ~/Library/Logs/chorus, falling back to the temp dir.
func logDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Library", "Logs", "chorus")
	}
	return os.TempDir()
}

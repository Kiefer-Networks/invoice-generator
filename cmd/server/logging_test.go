package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestApplicationLoggerUsesJSONInProduction(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := newApplicationLogger(false, &output)
	logger.Info("started", slog.String("component", "server"))
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("production log is not JSON: %v: %q", err, output.String())
	}
	if event["msg"] != "started" || event["component"] != "server" {
		t.Fatalf("unexpected structured event: %#v", event)
	}
}

func TestApplicationLoggerKeepsReadableDevelopmentOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	newApplicationLogger(true, &output).Info("started", slog.String("component", "server"))
	if bytes.HasPrefix(bytes.TrimSpace(output.Bytes()), []byte("{")) || !bytes.Contains(output.Bytes(), []byte("msg=started")) {
		t.Fatalf("development log is not readable text: %q", output.String())
	}
}

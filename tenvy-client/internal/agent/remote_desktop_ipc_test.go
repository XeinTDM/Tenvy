package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	remotedesktop "github.com/rootbay/tenvy-client/internal/modules/control/remotedesktop"
)

func TestRemoteDesktopIPCClientIntegration(t *testing.T) {
	workDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	outputDir := t.TempDir()
	pluginPath := filepath.Join(outputDir, "fake-plugin")
	if runtime.GOOS == "windows" {
		pluginPath += ".exe"
	}

	build := exec.Command("go", "build", "-o", pluginPath, "./testdata/fakeplugin")
	build.Dir = workDir
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake plugin: %v: %s", err, out)
	}

	logPath := filepath.Join(outputDir, "plugin-log.jsonl")
	t.Setenv("FAKE_REMOTE_DESKTOP_PLUGIN_LOG", logPath)

	logger := log.New(io.Discard, "", 0)
	engine := remotedesktop.NewManagedRemoteDesktopEngine(pluginPath, "test-version", nil, logger)

	cfg := remotedesktop.Config{
		AgentID: "agent-123",
		BaseURL: "https://controller.example",
		AuthKey: "test-key",
	}

	if err := engine.Configure(cfg); err != nil {
		t.Fatalf("configure engine: %v", err)
	}

	ctx := context.Background()
	startPayload := remotedesktop.RemoteDesktopCommandPayload{Action: "start", SessionID: "session-1"}
	if err := engine.StartSession(ctx, startPayload); err != nil {
		t.Fatalf("start session: %v", err)
	}

	updatePayload := remotedesktop.RemoteDesktopCommandPayload{Action: "configure", SessionID: "session-1"}
	if err := engine.UpdateSession(updatePayload); err != nil {
		t.Fatalf("update session: %v", err)
	}

	inputPayload := remotedesktop.RemoteDesktopCommandPayload{
		Action:    "input",
		SessionID: "session-1",
		Events: []remotedesktop.RemoteDesktopInputEvent{
			{
				Type:       remotedesktop.RemoteInputMouseMove,
				CapturedAt: time.Now().UnixNano(),
				X:          10,
				Y:          5,
			},
		},
	}
	if err := engine.HandleInput(ctx, inputPayload); err != nil {
		t.Fatalf("handle input: %v", err)
	}

	frame := remotedesktop.RemoteDesktopFramePacket{
		SessionID: "session-1",
		Width:     1,
		Height:    1,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Encoding:  "raw",
	}
	if err := engine.DeliverFrame(ctx, frame); err != nil {
		t.Fatalf("deliver frame: %v", err)
	}

	if err := engine.StopSession("session-1"); err != nil {
		t.Fatalf("stop session: %v", err)
	}

	engine.Shutdown()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read plugin log: %v", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	var methods []string
	for decoder.More() {
		var entry struct {
			Method string `json:"method"`
		}
		if err := decoder.Decode(&entry); err != nil {
			t.Fatalf("decode log entry: %v", err)
		}
		methods = append(methods, entry.Method)
	}

	expected := []string{
		"configure",
		"startSession",
		"updateSession",
		"handleInput",
		"deliverFrame",
		"stopSession",
		"shutdown",
	}

	if len(methods) != len(expected) {
		t.Fatalf("unexpected method count: got %d want %d (%v)", len(methods), len(expected), methods)
	}

	for i, method := range methods {
		if method != expected[i] {
			t.Fatalf("method %d mismatch: got %s want %s", i, method, expected[i])
		}
	}
}

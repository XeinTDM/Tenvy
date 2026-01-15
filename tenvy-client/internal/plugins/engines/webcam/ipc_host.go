package webcamengine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

type HTTPClientFactory func(timeout time.Duration) *http.Client

type ipcRequest struct {
	ID     uint64          `msgpack:"id"`
	Method string          `msgpack:"method"`
	Params msgpack.RawMessage `msgpack:"params"`
}

type ipcResponse struct {
	ID     uint64          `msgpack:"id"`
	Result msgpack.RawMessage `msgpack:"result,omitempty"`
	Error  *ipcError       `msgpack:"error,omitempty"`
}

type ipcError struct {
	Message string `msgpack:"message"`
}

const (
	methodConfigure     = "configure"
	methodHandleCommand = "handle_command"
	methodShutdown      = "shutdown"
)

type configEnvelope struct {
	AgentID        string        `msgpack:"agent_id"`
	BaseURL        string        `msgpack:"base_url"`
	AuthKey        string        `msgpack:"auth_key"`
	UserAgent      string        `msgpack:"user_agent"`
	RequestTimeout time.Duration `msgpack:"request_timeout"`
}

func (e configEnvelope) toConfig(logger Logger) Config {
	return Config{
		AgentID:        e.AgentID,
		BaseURL:        e.BaseURL,
		AuthKey:        e.AuthKey,
		UserAgent:      e.UserAgent,
		Logger:         logger,
		RequestTimeout: e.RequestTimeout,
	}
}

func ServeEngineIPC(ctx context.Context, engine Engine, reader io.Reader, writer io.Writer, logger Logger, clients HTTPClientFactory) error {
	if engine == nil {
		return errors.New("webcam engine not provided")
	}
	if reader == nil || writer == nil {
		return errors.New("ipc transport not configured")
	}
	if clients == nil {
		clients = func(timeout time.Duration) *http.Client {
			client := &http.Client{}
			if timeout > 0 {
				client.Timeout = timeout
			}
			return client
		}
	}

	dec := msgpack.NewDecoder(reader)
	bufWriter := bufio.NewWriter(writer)
	enc := msgpack.NewEncoder(bufWriter)

	var mu sync.Mutex
	handle := func(req ipcRequest) ipcResponse {
		mu.Lock()
		defer mu.Unlock()

		respond := ipcResponse{ID: req.ID}

		switch req.Method {
		case methodConfigure:
			var payload configEnvelope
			if err := msgpack.Unmarshal(req.Params, &payload); err != nil {
				respond.Error = &ipcError{Message: fmt.Sprintf("decode configure payload: %v", err)}
				return respond
			}
			cfg := payload.toConfig(logger)
			cfg.Client = clients(cfg.RequestTimeout)
			if err := engine.Configure(cfg); err != nil {
				respond.Error = &ipcError{Message: err.Error()}
			}
		case methodHandleCommand:
			var payload protocol.Command
			if err := msgpack.Unmarshal(req.Params, &payload); err != nil {
				respond.Error = &ipcError{Message: fmt.Sprintf("decode command payload: %v", err)}
				return respond
			}
			result := engine.HandleCommand(ctx, payload)
			data, err := msgpack.Marshal(result)
			if err != nil {
				respond.Error = &ipcError{Message: fmt.Sprintf("encode command result: %v", err)}
				return respond
			}
			respond.Result = data
		case methodShutdown:
			engine.Shutdown()
			respond.Result = msgpack.RawMessage(`{"status":"ok"}`)
			return respond
		default:
			respond.Error = &ipcError{Message: fmt.Sprintf("unknown method: %s", req.Method)}
		}

		if respond.Error == nil && respond.Result == nil {
			respond.Result = msgpack.RawMessage(`{"status":"ok"}`)
		}
		return respond
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var req ipcRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode ipc request: %w", err)
		}

		resp := handle(req)
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("encode ipc response: %w", err)
		}
		if err := bufWriter.Flush(); err != nil {
			return fmt.Errorf("flush ipc response: %w", err)
		}

		if req.Method == methodShutdown {
			return nil
		}
	}
}

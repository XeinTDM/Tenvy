package registryengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
	"github.com/vmihailenco/msgpack/v5"
)

type externalRegistryEngine struct {
	path    string
	version string
	logger  Logger

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	dec     *msgpack.Decoder
	enc     *msgpack.Encoder
	nextID  uint64
	pending map[uint64]chan *ipcResponse
}

func NewManagedRegistryEngine(path, version string, logger Logger) Engine {
	return &externalRegistryEngine{
		path:    path,
		version: version,
		logger:  logger,
		pending: make(map[uint64]chan *ipcResponse),
	}
}

func (e *externalRegistryEngine) Configure(cfg Config) error {
	envelope := configEnvelope{}
	_, err := e.call(context.Background(), methodConfigure, envelope)
	return err
}

func (e *externalRegistryEngine) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	resp, err := e.call(ctx, methodHandleCommand, cmd)
	if err != nil {
		return protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       err.Error(),
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}

	var result protocol.CommandResult
	if err := msgpack.Unmarshal(resp.Result, &result); err != nil {
		return protocol.CommandResult{
			CommandID:   cmd.ID,
			Success:     false,
			Error:       fmt.Sprintf("decode result: %v", err),
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}
	return result
}

func (e *externalRegistryEngine) Shutdown() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cmd == nil {
		return
	}

	_ = e.enc.Encode(ipcRequest{ID: e.nextID, Method: methodShutdown})
	e.nextID++

	done := make(chan struct{})
	go func() {
		e.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		e.cmd.Process.Kill()
	}

	e.stdin.Close()
	e.stdout.Close()
	e.cmd = nil
}

func (e *externalRegistryEngine) call(ctx context.Context, method string, params any) (*ipcResponse, error) {
	e.mu.Lock()
	if e.cmd == nil {
		if err := e.startLocked(); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}

	id := e.nextID
	e.nextID++

	paramData, err := msgpack.Marshal(params)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}

	req := ipcRequest{
		ID:     id,
		Method: method,
		Params: paramData,
	}

	ch := make(chan *ipcResponse, 1)
	e.pending[id] = ch
	
	err = e.enc.Encode(req)
	e.mu.Unlock()

	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Message)
		}
		return resp, nil
	}
}

func (e *externalRegistryEngine) startLocked() error {
	cmd := exec.Command(e.path)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return err
	}

	e.cmd = cmd
	e.stdin = stdin
	e.stdout = stdout
	e.enc = msgpack.NewEncoder(stdin)
	e.dec = msgpack.NewDecoder(stdout)

	go e.logStderr(stderr)
	go e.readLoop()

	return nil
}

func (e *externalRegistryEngine) logStderr(r io.ReadCloser) {
	defer r.Close()
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 && e.logger != nil {
			e.logger.Printf("external-registry: %s", string(buf[:n]))
		}
		if err != nil {
			break
		}
	}
}

func (e *externalRegistryEngine) readLoop() {
	for {
		var resp ipcResponse
		if err := e.dec.Decode(&resp); err != nil {
			break
		}

		e.mu.Lock()
		ch, ok := e.pending[resp.ID]
		if ok {
			delete(e.pending, resp.ID)
		}
		e.mu.Unlock()

		if ok {
			ch <- &resp
		}
	}
}

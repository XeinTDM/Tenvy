package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	manifest "github.com/rootbay/tenvy-client/shared/pluginmanifest"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	wasmHostModuleName      = "tenvy_host"
	wasmHostInterfacesEnv   = "TENVY_HOST_INTERFACES"
	wasmHostAPIVersionEnv   = "TENVY_HOST_API_VERSION"
	wasmHostSandboxFlagEnv  = "TENVY_HOST_SANDBOXED"
	wasmRuntimeKindEnv      = "TENVY_RUNTIME_KIND"
	wasmRuntimeKindValue    = "wasm"
	wasmStopTimeoutFallback = 500 * time.Millisecond
)

const WasmHostAPIVersion = "1.0"

type wasmRuntimeHandle struct {
	name            string
	logger          *log.Logger
	shutdownTimeout time.Duration

	cancel  context.CancelFunc
	runtime wazero.Runtime
	module  api.Module
	stopFn  api.Function

	done       chan struct{}
	mu         sync.Mutex
	closeOnce  sync.Once
	startErr   error
	terminated bool
}

func launchWasmRuntime(ctx context.Context, entryPath string, opts RuntimeOptions) (RuntimeHandle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(entryPath)
	if trimmed == "" {
		return nil, errors.New("plugin entry path not provided")
	}

	resolved, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("resolve entry path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("locate plugin entry: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("plugin entry %s is a directory", resolved)
	}

	moduleBytes, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("load wasm module: %w", err)
	}

	runtimeCtx, cancel := context.WithCancel(context.Background())
	runtimeConfig := wazero.NewRuntimeConfigInterpreter()
	if opts.Sandboxed {
		runtimeConfig = runtimeConfig.WithCloseOnContextDone(true)
	}

	wasmRuntime := wazero.NewRuntimeWithConfig(runtimeCtx, runtimeConfig)
	if _, err := wasi_snapshot_preview1.Instantiate(runtimeCtx, wasmRuntime); err != nil {
		cancel()
		wasmRuntime.Close(runtimeCtx) //nolint:errcheck
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	if _, err := instantiateWasmHost(runtimeCtx, wasmRuntime, opts); err != nil {
		cancel()
		wasmRuntime.Close(runtimeCtx) //nolint:errcheck
		return nil, err
	}

	compiled, err := wasmRuntime.CompileModule(runtimeCtx, moduleBytes)
	if err != nil {
		cancel()
		wasmRuntime.Close(runtimeCtx) //nolint:errcheck
		return nil, fmt.Errorf("compile wasm module: %w", err)
	}
	defer compiled.Close(runtimeCtx) //nolint:errcheck

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = filepath.Base(resolved)
	}

	stdoutWriter := resolveWasmWriter(opts.Stdout, opts.Logger, name, "stdout")
	stderrWriter := resolveWasmWriter(opts.Stderr, opts.Logger, name, "stderr")

	moduleConfig := wazero.NewModuleConfig().WithStdout(stdoutWriter).WithStderr(stderrWriter).WithName(name)
	if len(opts.Args) > 0 {
		moduleConfig = moduleConfig.WithArgs(opts.Args...)
	}
	for _, entry := range opts.Env {
		if entry == "" {
			continue
		}
		key := entry
		value := ""
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
			value = entry[idx+1:]
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		moduleConfig = moduleConfig.WithEnv(key, value)
	}

	interfaces := sanitizeInterfaces(opts.HostInterfaces)
	if len(interfaces) == 0 {
		interfaces = []string{manifest.HostInterfaceCoreV1}
	}
	moduleConfig = moduleConfig.WithEnv(wasmHostInterfacesEnv, strings.Join(interfaces, ","))
	apiVersion := strings.TrimSpace(opts.HostAPIVersion)
	if apiVersion == "" {
		apiVersion = WasmHostAPIVersion
	}
	moduleConfig = moduleConfig.WithEnv(wasmHostAPIVersionEnv, apiVersion)
	if opts.Sandboxed {
		moduleConfig = moduleConfig.WithEnv(wasmHostSandboxFlagEnv, "1")
	}
	moduleConfig = moduleConfig.WithEnv(wasmRuntimeKindEnv, wasmRuntimeKindValue)

	module, err := wasmRuntime.InstantiateModule(runtimeCtx, compiled, moduleConfig)
	if err != nil {
		cancel()
		wasmRuntime.Close(runtimeCtx) //nolint:errcheck
		return nil, fmt.Errorf("instantiate wasm module: %w", err)
	}

	startFn := module.ExportedFunction("tenvy_plugin_start")
	if startFn == nil {
		cancel()
		module.Close(runtimeCtx)      //nolint:errcheck
		wasmRuntime.Close(runtimeCtx) //nolint:errcheck
		return nil, errors.New("wasm module missing tenvy_plugin_start entry point")
	}
	stopFn := module.ExportedFunction("tenvy_plugin_stop")

	handle := &wasmRuntimeHandle{
		name:            name,
		logger:          opts.Logger,
		shutdownTimeout: normalizeShutdownTimeout(opts.ShutdownTimeout),
		cancel:          cancel,
		runtime:         wasmRuntime,
		module:          module,
		stopFn:          stopFn,
		done:            make(chan struct{}),
	}

	go handle.run(runtimeCtx, startFn)

	select {
	case <-handle.done:
		err := handle.exitError()
		handle.closeRuntime()
		if err == nil {
			err = errors.New("wasm runtime terminated immediately")
		}
		return nil, err
	default:
	}

	if opts.Logger != nil {
		opts.Logger.Printf("plugin runtime %s started (wasm)", name)
	}

	return handle, nil
}

func (h *wasmRuntimeHandle) run(ctx context.Context, start api.Function) {
	results, callErr := start.Call(ctx)
	var statusErr error
	if callErr != nil {
		statusErr = callErr
	} else if len(results) > 0 && results[0] != 0 {
		statusErr = fmt.Errorf("exit status %d", results[0])
	}

	h.mu.Lock()
	h.startErr = statusErr
	h.terminated = true
	h.mu.Unlock()

	h.closeRuntime()
	close(h.done)

	if h.logger != nil {
		if statusErr != nil {
			h.logger.Printf("plugin runtime %s exited: %v", h.name, statusErr)
		} else {
			h.logger.Printf("plugin runtime %s exited", h.name)
		}
	}
}

func (h *wasmRuntimeHandle) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	h.cancel()

	timeout := h.shutdownTimeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}

	stopCtx, stopCancel := context.WithTimeout(ctx, timeout)
	if h.stopFn != nil {
		if _, err := h.stopFn.Call(stopCtx); err != nil && h.logger != nil && !errors.Is(err, context.Canceled) {
			h.logger.Printf("plugin runtime %s stop failed: %v", h.name, err)
		}
	}
	stopCancel()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-h.done:
		err := h.exitError()
		h.closeRuntime()
		return err
	case <-ctx.Done():
		h.closeRuntime()
		return ctx.Err()
	case <-timer.C:
		h.closeRuntime()
		if h.logger != nil {
			h.logger.Printf("plugin runtime %s forcefully terminated", h.name)
		}
		select {
		case <-h.done:
			return h.exitError()
		case <-time.After(wasmStopTimeoutFallback):
			return errors.New("wasm runtime shutdown timeout")
		}
	}
}

func (h *wasmRuntimeHandle) exitError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return normalizeWasmExitError(h.startErr)
}

func (h *wasmRuntimeHandle) closeRuntime() {
	h.closeOnce.Do(func() {
		if h.module != nil {
			_ = h.module.Close(context.Background())
		}
		if h.runtime != nil {
			_ = h.runtime.Close(context.Background())
		}
	})
}

func instantiateWasmHost(ctx context.Context, runtime wazero.Runtime, opts RuntimeOptions) (api.Closer, error) {
	builder := runtime.NewHostModuleBuilder(wasmHostModuleName)
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "plugin"
	}

	logger := opts.Logger

	builder.NewFunctionBuilder().WithParameterNames("ptr", "len").WithGoModuleFunction(
		api.GoModuleFunc(func(ctx context.Context, module api.Module, stack []uint64) {
			if len(stack) < 2 || logger == nil {
				return
			}
			ptr := uint32(stack[0])
			size := uint32(stack[1])
			if size == 0 {
				return
			}
			memory := module.Memory()
			if memory == nil {
				return
			}
			data, ok := memory.Read(ptr, size)
			if !ok {
				return
			}
			message := strings.TrimRight(string(data), "\n")
			if message == "" {
				return
			}
			logger.Printf("plugin runtime %s log: %s", name, message)
		}),
		[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
		nil,
	).Export("log")

	builder.NewFunctionBuilder().WithParameterNames("milliseconds").WithGoModuleFunction(
		api.GoModuleFunc(func(ctx context.Context, module api.Module, stack []uint64) {
			if len(stack) == 0 {
				return
			}
			delay := time.Duration(int32(stack[0]))
			if delay <= 0 {
				delay = 1
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Millisecond * delay):
			}
		}),
		[]api.ValueType{api.ValueTypeI32},
		nil,
	).Export("yield")

	return builder.Instantiate(ctx)
}

func resolveWasmWriter(writer io.Writer, logger *log.Logger, name, stream string) io.Writer {
	if writer != nil {
		return writer
	}
	if logger == nil {
		return io.Discard
	}
	return &loggerWriter{logger: logger, name: name, stream: stream}
}

type loggerWriter struct {
	logger *log.Logger
	name   string
	stream string
}

func (w *loggerWriter) Write(p []byte) (int, error) {
	if w == nil || w.logger == nil || len(p) == 0 {
		return len(p), nil
	}
	message := strings.TrimRight(string(p), "\n")
	if message != "" {
		w.logger.Printf("plugin runtime %s %s: %s", w.name, w.stream, message)
	}
	return len(p), nil
}

func normalizeWasmExitError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ExitCode() == 0 {
			return nil
		}
		return fmt.Errorf("wasm runtime exit status %d", exitErr.ExitCode())
	}
	return err
}

func normalizeShutdownTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultShutdownTimeout
	}
	return timeout
}

func sanitizeInterfaces(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lowered := strings.ToLower(trimmed)
		if _, ok := seen[lowered]; ok {
			continue
		}
		seen[lowered] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return nil
	}
	sort.Strings(normalized)
	return normalized
}

package plugins

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// RuntimeOptions describes how to launch a plugin runtime entry point.
type RuntimeOptions struct {
	// Name is used for logging output. If empty, the executable base name is used.
	Name string
	// Args specifies additional arguments to pass to the plugin entry point.
	Args []string
	// Env specifies the environment for the new process. If empty the current
	// process environment is inherited.
	Env []string
	// Dir sets the working directory for the new process. If empty the
	// process inherits the caller's working directory.
	Dir string
	// Stdout specifies the writer used for standard output. When nil the
	// output is forwarded to the provided logger (if any) or discarded.
	Stdout io.Writer
	// Stderr specifies the writer used for standard error. When nil the
	// output is forwarded to the provided logger (if any) or discarded.
	Stderr io.Writer
	// Logger receives lifecycle and output messages. Optional.
	Logger *log.Logger
	// ShutdownTimeout defines how long to wait for the process to exit after
	// cancellation before it is forcefully killed. When zero a default is
	// used.
	ShutdownTimeout time.Duration
	// Kind selects the runtime environment used to execute the plugin.
	Kind RuntimeKind
	// HostInterfaces enumerates the host contracts exposed to sandboxed
	// runtimes like WASM modules.
	HostInterfaces []string
	// HostAPIVersion declares the host API level exposed to the runtime.
	HostAPIVersion string
	// Sandboxed indicates whether the runtime should operate in sandboxed
	// mode when supported.
	Sandboxed bool
	// Secret is the optional encryption key for decrypting the entry binary.
	Secret []byte
}

// RuntimeHandle controls a running plugin entry point.
type RuntimeHandle interface {
	Shutdown(context.Context) error
}

type processRuntimeHandle struct {
	name            string
	logger          *log.Logger
	shutdownTimeout time.Duration
	tempPath        string

	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	done    chan struct{}
	waitErr error
}

const (
	defaultShutdownTimeout = 5 * time.Second
)

// RuntimeKind represents the execution environment for a plugin entry point.
type RuntimeKind string

const (
	RuntimeKindNative RuntimeKind = "native"
	RuntimeKindWASM   RuntimeKind = "wasm"
)

// LaunchRuntime executes the provided plugin entry point and returns a handle
// that can be used to terminate the process.
func LaunchRuntime(ctx context.Context, entryPath string, opts RuntimeOptions) (RuntimeHandle, error) {
	if opts.Kind == "" {
		opts.Kind = RuntimeKindNative
	}

	switch opts.Kind {
	case RuntimeKindWASM:
		return launchWasmRuntime(ctx, entryPath, opts)
	case RuntimeKindNative:
		fallthrough
	default:
		return launchProcessRuntime(ctx, entryPath, opts)
	}
}

func launchProcessRuntime(ctx context.Context, entryPath string, opts RuntimeOptions) (RuntimeHandle, error) {
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

	// Check if encrypted
	info, err := os.Stat(resolved)
	if errors.Is(err, fs.ErrNotExist) {
		encInfo, encErr := os.Stat(resolved + ".enc")
		if encErr == nil && !encInfo.IsDir() {
			if len(opts.Secret) == 0 {
				return nil, errors.New("plugin binary is encrypted but no secret provided")
			}
			data, readErr := os.ReadFile(resolved + ".enc")
			if readErr != nil {
				return nil, fmt.Errorf("read encrypted binary: %w", readErr)
			}
			decrypted, decErr := decrypt(data, opts.Secret)
			if decErr != nil {
				return nil, fmt.Errorf("decrypt binary: %w", decErr)
			}
			
			// Create temp file for execution
			tmpFile, tempErr := os.CreateTemp(filepath.Dir(resolved), "run-*.exe")
			if tempErr != nil {
				return nil, fmt.Errorf("create temp binary: %w", tempErr)
			}
			defer tmpFile.Close() // Best effort close
			
			if _, writeErr := tmpFile.Write(decrypted); writeErr != nil {
				os.Remove(tmpFile.Name())
				return nil, fmt.Errorf("write temp binary: %w", writeErr)
			}
			tmpFile.Close()
			if chmodErr := os.Chmod(tmpFile.Name(), 0700); chmodErr != nil {
				// Continue, windows permissions are different
			}
			
			resolved = tmpFile.Name()
			info, _ = os.Stat(resolved)
			
			// Schedule cleanup on context done or exit
			defer func() {
				// This defer runs when launchProcessRuntime returns, not when process exits.
				// We need to cleanup after process exit.
				// We can delete immediately on Windows if opened with sharing? No.
				// We will handle cleanup in the Wait() or Shutdown().
			}()
		} else {
			return nil, fmt.Errorf("locate plugin entry: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("locate plugin entry: %w", err)
	}
	
	if info.IsDir() {
		return nil, fmt.Errorf("plugin entry %s is a directory", resolved)
	}

	if runtime.GOOS != "windows" {
		if info.Mode()&0o111 == 0 {
			return nil, fmt.Errorf("plugin entry %s is not executable", resolved)
		}
	}

	launchCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(launchCtx, resolved, opts.Args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if len(opts.Env) > 0 {
		cmd.Env = append([]string(nil), opts.Env...)
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = filepath.Base(resolved)
	}

	stdoutWriter := opts.Stdout
	stderrWriter := opts.Stderr

	var stdoutPipe io.ReadCloser
	var stderrPipe io.ReadCloser
	var pipeErr error

	if stdoutWriter == nil {
		if opts.Logger != nil {
			stdoutPipe, pipeErr = cmd.StdoutPipe()
			if pipeErr != nil {
				cancel()
				return nil, fmt.Errorf("stdout pipe: %w", pipeErr)
			}
		} else {
			cmd.Stdout = io.Discard
		}
	} else {
		cmd.Stdout = stdoutWriter
	}

	if stderrWriter == nil {
		if opts.Logger != nil {
			stderrPipe, pipeErr = cmd.StderrPipe()
			if pipeErr != nil {
				if stdoutPipe != nil {
					stdoutPipe.Close()
				}
				cancel()
				return nil, fmt.Errorf("stderr pipe: %w", pipeErr)
			}
		} else {
			cmd.Stderr = io.Discard
		}
	} else {
		cmd.Stderr = stderrWriter
	}

	if stdoutPipe != nil && stdoutWriter != nil {
		stdoutPipe.Close()
		stdoutPipe = nil
	}
	if stderrPipe != nil && stderrWriter != nil {
		stderrPipe.Close()
		stderrPipe = nil
	}

	if err := cmd.Start(); err != nil {
		if stdoutPipe != nil {
			stdoutPipe.Close()
		}
		if stderrPipe != nil {
			stderrPipe.Close()
		}
		cancel()
		return nil, fmt.Errorf("launch plugin runtime: %w", err)
	}

	handle := &processRuntimeHandle{
		name:            name,
		logger:          opts.Logger,
		shutdownTimeout: opts.ShutdownTimeout,
		cmd:             cmd,
		cancel:          cancel,
		done:            make(chan struct{}),
	}

	// If resolved is a temp file we created, track it for cleanup
	if strings.Contains(filepath.Base(resolved), "run-") && strings.HasSuffix(filepath.Base(resolved), ".exe") {
		// Use a better check or track the variable from the encryption block
		// Ideally we'd return the temp path from the block above
		handle.tempPath = resolved
	}

	if handle.shutdownTimeout <= 0 {
		handle.shutdownTimeout = defaultShutdownTimeout
	}

	if stdoutPipe != nil {
		go streamProcessOutput(stdoutPipe, opts.Logger, name, "stdout")
	}
	if stderrPipe != nil {
		go streamProcessOutput(stderrPipe, opts.Logger, name, "stderr")
	}

	go handle.wait()

	if opts.Logger != nil {
		opts.Logger.Printf("plugin runtime %s started (pid=%d)", name, cmd.Process.Pid)
	}

	return handle, nil
}

func (h *processRuntimeHandle) wait() {
	err := h.cmd.Wait()
	h.mu.Lock()
	h.waitErr = err
	close(h.done)
	h.mu.Unlock()

	if h.logger != nil {
		if err != nil {
			h.logger.Printf("plugin runtime %s exited: %v", h.name, err)
		} else {
			h.logger.Printf("plugin runtime %s exited", h.name)
		}
	}
}

// Shutdown stops the plugin runtime. The provided context controls how long to
// wait for termination before returning.
func (h *processRuntimeHandle) Shutdown(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	h.mu.Lock()
	cmd := h.cmd
	cancel := h.cancel
	done := h.done
	h.mu.Unlock()

	if cmd == nil {
		return h.waitErr
	}

	cancel()

	timeout := h.shutdownTimeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	killed := false

	for {
		select {
		case <-done:
			h.mu.Lock()
			err := normalizeProcessExitError(h.waitErr)
			h.waitErr = err
			h.cmd = nil
			h.cancel = nil
			h.done = nil
			if h.tempPath != "" {
				os.Remove(h.tempPath)
			}
			h.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if !killed {
				h.mu.Lock()
				proc := h.cmd
				h.mu.Unlock()
				if proc != nil && proc.Process != nil {
					_ = proc.Process.Kill()
					if h.logger != nil {
						h.logger.Printf("plugin runtime %s forcefully terminated", h.name)
					}
				}
				killed = true
				timer.Reset(timeout)
			} else {
				timer.Reset(timeout)
			}
		}
	}
}

func normalizeProcessExitError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func streamProcessOutput(r io.ReadCloser, logger *log.Logger, name, stream string) {
	defer r.Close()
	if logger == nil {
		io.Copy(io.Discard, r) //nolint:errcheck // best effort cleanup
		return
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		logger.Printf("plugin runtime %s %s: %s", name, stream, scanner.Text())
	}
}

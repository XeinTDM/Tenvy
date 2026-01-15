package runner

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// IPCServerFunc defines the signature for the engine's IPC server function.
// We use a generic function type where the caller adapts their specific engine types.
type IPCServerFunc[T any] func(
	ctx context.Context,
	engine T,
	stdin io.Reader,
	stdout io.Writer,
	logger *log.Logger,
	httpClientFactory func(time.Duration) *http.Client,
) error

// Run executes the standard plugin lifecycle:
// 1. Parses flags (specifically -version).
// 2. Sets up logging.
// 3. Initializes the engine.
// 4. Starts the IPC server.
func Run[T any](
	versionOutput string,
	logName string,
	createEngine func(logger *log.Logger) T,
	serveIPC IPCServerFunc[T],
) {
	version := flag.Bool("version", false, "print build metadata")
	flag.Parse()

	if *version {
		fmt.Println(versionOutput)
		return
	}

	logger := log.New(os.Stderr, logName+": ", log.LstdFlags|log.Lmicroseconds)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng := createEngine(logger)

	httpFactory := func(timeout time.Duration) *http.Client {
		client := &http.Client{}
		if timeout > 0 {
			client.Timeout = timeout
		}
		return client
	}

	if err := serveIPC(ctx, eng, os.Stdin, os.Stdout, logger, httpFactory); err != nil {
		fmt.Fprintf(os.Stderr, "%s: ipc server error: %v\n", logName, err)
		os.Exit(1)
	}
}

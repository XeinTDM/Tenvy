package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"time"

	engine "github.com/rootbay/tenvy-client/internal/plugins/engines/tcpconnections"
	"github.com/rootbay/tenvy-client/internal/plugins/runner"
)

func main() {
	runner.Run(
		"tcpconnections-control-engine plugin",
		"tcpconnections-engine",
		func(logger *log.Logger) engine.Engine {
			return engine.NewTcpConnectionsEngine(engine.Config{Logger: logger})
		},
		func(ctx context.Context, eng engine.Engine, stdin io.Reader, stdout io.Writer, logger *log.Logger, httpFactory func(time.Duration) *http.Client) error {
			return engine.ServeEngineIPC(ctx, eng, stdin, stdout, logger, engine.HTTPClientFactory(httpFactory))
		},
	)
}
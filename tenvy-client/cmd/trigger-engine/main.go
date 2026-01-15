package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	engine "github.com/rootbay/tenvy-client/internal/plugins/engines/trigger"
)

func main() {
	version := flag.Bool("version", false, "print build metadata")
	flag.Parse()

	if *version {
		fmt.Println("trigger-control-engine plugin")
		return
	}

	logger := log.New(os.Stderr, "trigger-engine: ", log.LstdFlags|log.Lmicroseconds)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	triggerEngine := engine.NewTriggerEngine(engine.Config{Logger: logger})

	httpFactory := func(timeout time.Duration) *http.Client {
		client := &http.Client{}
		if timeout > 0 {
			client.Timeout = timeout
		}
		return client
	}

	if err := engine.ServeEngineIPC(ctx, triggerEngine, os.Stdin, os.Stdout, logger, httpFactory); err != nil {
		fmt.Fprintf(os.Stderr, "trigger-engine: ipc server error: %v\n", err)
		os.Exit(1)
	}
}


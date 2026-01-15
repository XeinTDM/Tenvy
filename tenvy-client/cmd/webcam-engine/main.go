package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	engine "github.com/rootbay/tenvy-client/internal/plugins/engines/webcam"
)

func main() {
	version := flag.Bool("version", false, "print build metadata")
	flag.Parse()

	if *version {
		fmt.Println("webcam-control-engine plugin")
		return
	}

	logger := log.New(os.Stderr, "webcam-engine: ", log.LstdFlags|log.Lmicroseconds)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	webcamEngine := engine.NewWebcamEngine(engine.Config{Logger: logger})

	httpFactory := func(timeout time.Duration) *http.Client {
		client := &http.Client{}
		if timeout > 0 {
			client.Timeout = timeout
		}
		return client
	}

	if err := engine.ServeEngineIPC(ctx, webcamEngine, os.Stdin, os.Stdout, logger, httpFactory); err != nil {
		fmt.Fprintf(os.Stderr, "webcam-engine: ipc server error: %v\n", err)
		os.Exit(1)
	}
}


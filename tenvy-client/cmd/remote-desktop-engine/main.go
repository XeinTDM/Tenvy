package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	engine "github.com/rootbay/tenvy-client/internal/plugins/engines/remotedesktop"
)

func main() {
	version := flag.Bool("version", false, "print build metadata")
	flag.Parse()

	if *version {
		fmt.Println("remote-desktop-engine plugin")
		return
	}

	logger := log.New(os.Stderr, "remote-desktop-engine: ", log.LstdFlags|log.Lmicroseconds)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streamer := engine.NewRemoteDesktopStreamer(engine.Config{Logger: logger})

	httpFactory := func(timeout time.Duration) *http.Client {
		client := &http.Client{}
		if timeout > 0 {
			client.Timeout = timeout
		}
		return client
	}

	if err := engine.ServeEngineIPC(ctx, streamer, os.Stdin, os.Stdout, logger, httpFactory); err != nil {
		fmt.Fprintf(os.Stderr, "remote-desktop-engine: ipc server error: %v\n", err)
		os.Exit(1)
	}
}
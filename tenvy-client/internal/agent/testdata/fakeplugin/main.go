package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	methodConfigure     = "configure"
	methodStartSession  = "startSession"
	methodStopSession   = "stopSession"
	methodUpdateSession = "updateSession"
	methodHandleInput   = "handleInput"
	methodDeliverFrame  = "deliverFrame"
	methodShutdown      = "shutdown"
)

type ipcRequest struct {
	ID     uint64             `json:"id" msgpack:"id"`
	Method string             `json:"method" msgpack:"method"`
	Params msgpack.RawMessage `json:"params,omitempty" msgpack:"params,omitempty"`
}

type ipcResponse struct {
	ID     uint64                 `json:"id" msgpack:"id"`
	Result map[string]interface{} `json:"result,omitempty" msgpack:"result,omitempty"`
	Error  *ipcError              `json:"error,omitempty" msgpack:"error,omitempty"`
}

type ipcError struct {
	Message string `json:"message" msgpack:"message"`
}

type logEntry struct {
	Method    string      `json:"method"`
	Timestamp string      `json:"timestamp"`
	Params    interface{} `json:"params,omitempty"`
}

func main() {
	logPath := os.Getenv("FAKE_REMOTE_DESKTOP_PLUGIN_LOG")
	var logEncoder *json.Encoder
	if logPath != "" {
		file, err := os.Create(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake plugin: open log: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()
		logEncoder = json.NewEncoder(file)
	}

	decoder := msgpack.NewDecoder(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	encoder := msgpack.NewEncoder(writer)

	for {
		var req ipcRequest
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			fmt.Fprintf(os.Stderr, "fake plugin: decode request: %v\n", err)
			return
		}

		if logEncoder != nil {
			var params interface{}
			if len(req.Params) > 0 {
				if err := msgpack.Unmarshal(req.Params, &params); err != nil {
					fmt.Fprintf(os.Stderr, "fake plugin: decode params for log: %v\n", err)
				}
			}

			entry := logEntry{
				Method:    req.Method,
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Params:    params,
			}
			_ = logEncoder.Encode(entry)
		}

		resp := ipcResponse{ID: req.ID, Result: map[string]interface{}{"status": "ok"}}
		if err := encoder.Encode(resp); err != nil {
			fmt.Fprintf(os.Stderr, "fake plugin: encode response: %v\n", err)
			return
		}
		if err := writer.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "fake plugin: flush response: %v\n", err)
			return
		}

		if req.Method == methodShutdown {
			return
		}
	}
}

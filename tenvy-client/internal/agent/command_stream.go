package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
	"nhooyr.io/websocket"
)

const (
	commandStreamDialTimeout    = 15 * time.Second
	remoteDesktopInputQueueSize = 32
	sessionTokenHeader          = "X-Agent-Session-Token"
)

type remoteDesktopInputTask struct {
	ctx    context.Context
	module *remoteDesktopModule
	burst  protocol.RemoteDesktopInputBurst
}

type sessionTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

func (a *Agent) sessionTokenURL() (string, error) {
	if a == nil {
		return "", errors.New("agent not initialized")
	}
	base := strings.TrimSpace(a.baseURL)
	if base == "" {
		return "", errors.New("missing base url")
	}
	if strings.TrimSpace(a.id) == "" {
		return "", errors.New("missing agent identifier")
	}

	joined, err := url.JoinPath(base, "api", "agents", url.PathEscape(a.id), "session-token")
	if err != nil {
		return "", err
	}

	parsed, err := url.Parse(joined)
	if err != nil {
		return "", err
	}

	if strings.ToLower(parsed.Scheme) != "https" && strings.ToLower(parsed.Scheme) != "http" {
		return "", fmt.Errorf("unsupported session token scheme: %s", parsed.Scheme)
	}

	return parsed.String(), nil
}

func (a *Agent) commandStreamURL() (string, error) {
	if a == nil {
		return "", errors.New("agent not initialized")
	}
	base := strings.TrimSpace(a.baseURL)
	if base == "" {
		return "", errors.New("missing base url")
	}
	if strings.TrimSpace(a.id) == "" {
		return "", errors.New("missing agent identifier")
	}

	joined, err := url.JoinPath(base, "api", "agents", url.PathEscape(a.id), "session")
	if err != nil {
		return "", err
	}

	parsed, err := url.Parse(joined)
	if err != nil {
		return "", err
	}

	switch strings.ToLower(parsed.Scheme) {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
		// Ignore
	default:
		return "", fmt.Errorf("unsupported command stream scheme: %s", parsed.Scheme)
	}

	return parsed.String(), nil
}

func (a *Agent) fetchSessionToken(ctx context.Context) (string, error) {
	if a == nil {
		return "", errors.New("agent not initialized")
	}

	endpoint, err := a.sessionTokenURL()
	if err != nil {
		return "", err
	}

	trimmedKey := strings.TrimSpace(a.key)
	if trimmedKey == "" {
		return "", errors.New("missing agent key")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", a.userAgent())
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", trimmedKey))
	applyRequestDecorations(req, a.requestHeaders, a.requestCookies)

	client := a.client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		limited := io.LimitReader(resp.Body, 1024)
		body, _ := io.ReadAll(limited)
		message := strings.TrimSpace(string(body))
		if resp.StatusCode == http.StatusUnauthorized {
			if message == "" {
				return "", protocol.ErrUnauthorized
			}
			return "", fmt.Errorf("%w: %s", protocol.ErrUnauthorized, message)
		}
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return "", fmt.Errorf("session token request failed: %s", message)
	}

	var payload sessionTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}

	token := strings.TrimSpace(payload.Token)
	if token == "" {
		return "", errors.New("session token missing in response")
	}

	if payload.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, payload.ExpiresAt); err != nil {
			if a.logger != nil {
				a.logger.Printf("invalid session token expiry: %v", err)
			}
		}
	}

	return token, nil
}

func (a *Agent) runCommandStream(ctx context.Context) {
	if a == nil {
		return
	}

	backoff := a.pollInterval()

	for {
		if ctx.Err() != nil {
			return
		}

		streamURL, err := a.commandStreamURL()
		if err != nil {
			if a.logger != nil {
				a.logger.Printf("command stream unavailable: %v", err)
			}
			return
		}

		token, err := a.fetchSessionToken(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, protocol.ErrUnauthorized) {
				a.requestReconnect()
			}
			if a.logger != nil && !errors.Is(err, context.Canceled) {
				a.logger.Printf("session token request failed: %v", err)
			}
			if err := sleepContext(ctx, backoff); err != nil {
				return
			}
			backoff = minDuration(backoff*2, a.maxBackoff())
			continue
		}

		headers := http.Header{}
		headers.Set("User-Agent", a.userAgent())
		headers.Set(sessionTokenHeader, token)
		applyHeaderMapDecorations(headers, a.requestHeaders, a.requestCookies)

		dialCtx, cancel := context.WithTimeout(ctx, commandStreamDialTimeout)
		dialOptions := &websocket.DialOptions{
			HTTPHeader:      headers,
			Subprotocols:    []string{protocol.CommandStreamSubprotocol},
			CompressionMode: websocket.CompressionContextTakeover,
		}
		if a.client != nil {
			dialOptions.HTTPClient = a.client
		}
		conn, resp, err := websocket.Dial(dialCtx, streamURL, dialOptions)
		cancel()
		if err != nil {
			if resp != nil {
				resp.Body.Close()
				if shouldReRegisterStatus(resp.StatusCode) {
					if a.logger != nil {
						a.logger.Printf("command stream rejected with status %d; scheduling re-registration", resp.StatusCode)
					}
					a.requestReconnect()
				}
			}
			if a.logger != nil && !errors.Is(err, context.Canceled) {
				a.logger.Printf("command stream connection failed: %v", err)
			}
			if err := sleepContext(ctx, backoff); err != nil {
				return
			}
			backoff = minDuration(backoff*2, a.maxBackoff())
			continue
		}

		if selected := conn.Subprotocol(); selected != protocol.CommandStreamSubprotocol {
			if a.logger != nil {
				if selected == "" {
					a.logger.Printf("command stream rejected connection without subprotocol")
				} else {
					a.logger.Printf("command stream negotiated unexpected subprotocol %q", selected)
				}
			}
			_ = conn.Close(websocket.StatusPolicyViolation, "unsupported protocol")
			if err := sleepContext(ctx, backoff); err != nil {
				return
			}
			backoff = minDuration(backoff*2, a.maxBackoff())
			continue
		}

		conn.SetReadLimit(protocol.CommandStreamMaxMessageSize)
		backoff = a.pollInterval()
		if a.logger != nil {
			a.logger.Printf("command stream connected")
		}

		a.consumeCommandStream(ctx, conn)

		if a.logger != nil {
			a.logger.Printf("command stream disconnected")
		}

		_ = conn.Close(websocket.StatusNormalClosure, "stream terminated")
	}
}

func (a *Agent) consumeCommandStream(ctx context.Context, conn *websocket.Conn) {
	if conn == nil {
		return
	}

	for {
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			status := websocket.CloseStatus(err)
			if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway && !errors.Is(err, context.Canceled) {
				if a.logger != nil {
					a.logger.Printf("command stream read error: %v", err)
				}
			}
			return
		}

		if messageType != websocket.MessageText {
			continue
		}

		var envelope protocol.CommandEnvelope
		var rawType struct {
			Type string `json:"type"`
		}
		
		if err := json.Unmarshal(data, &rawType); err != nil {
			continue
		}

		processedData := data
		if strings.ToLower(strings.TrimSpace(rawType.Type)) == "encrypted" {
			var encrypted protocol.EncryptedEnvelope
			if err := json.Unmarshal(data, &encrypted); err != nil {
				if a.logger != nil {
					a.logger.Printf("invalid encrypted envelope: %v", err)
				}
				continue
			}

			packet, err := base64.StdEncoding.DecodeString(encrypted.Data)
			if err != nil {
				if a.logger != nil {
					a.logger.Printf("failed to decode base64 encrypted data: %v", err)
				}
				continue
			}

			decrypted, err := a.decrypt(packet)
			if err != nil {
				if a.logger != nil {
					a.logger.Printf("failed to decrypt command envelope: %v", err)
				}
				continue
			}
			processedData = decrypted
		}

		if err := json.Unmarshal(processedData, &envelope); err != nil {
			if a.logger != nil {
				a.logger.Printf("invalid command envelope: %v", err)
			}
			continue
		}

		switch strings.ToLower(strings.TrimSpace(envelope.Type)) {
		case "command":
			if envelope.Command == nil {
				continue
			}
			a.processCommands(ctx, []protocol.Command{*envelope.Command})
		case "remote-desktop-input":
			if envelope.Input == nil {
				continue
			}
			a.handleRemoteDesktopInput(ctx, *envelope.Input)
		case "app-vnc-input":
			if envelope.AppVncInput == nil {
				continue
			}
			a.handleAppVncInput(ctx, *envelope.AppVncInput)
		default:
			continue
		}
	}
}

func (a *Agent) handleRemoteDesktopInput(ctx context.Context, burst protocol.RemoteDesktopInputBurst) {
	if a == nil || len(burst.Events) == 0 {
		return
	}
	if a.remoteDesktopInputStopped.Load() {
		return
	}
	if a.modules == nil {
		return
	}

	module := a.modules.remoteDesktopModule()
	if module == nil {
		if a.logger != nil {
			a.logger.Printf("remote desktop input ignored: module unavailable")
		}
		return
	}

	queue := a.ensureRemoteDesktopInputWorker()
	if queue == nil {
		return
	}

	stop := a.remoteDesktopInputStopSignal()
	if stop == nil {
		return
	}

	task := remoteDesktopInputTask{ctx: ctx, module: module, burst: burst}

	select {
	case <-stop:
		return
	case queue <- task:
	case <-ctx.Done():
		if a.logger != nil && !errors.Is(ctx.Err(), context.Canceled) {
			a.logger.Printf("remote desktop input dropped: %v", ctx.Err())
		}
	}
}

func (a *Agent) ensureRemoteDesktopInputWorker() chan remoteDesktopInputTask {
	if a == nil {
		return nil
	}

	if a.remoteDesktopInputStopped.Load() {
		return nil
	}

	a.remoteDesktopInputOnce.Do(func() {
		a.remoteDesktopInputQueue = make(chan remoteDesktopInputTask, remoteDesktopInputQueueSize)
		stop := a.remoteDesktopInputStopSignal()
		go a.remoteDesktopInputWorker(a.remoteDesktopInputQueue, stop)
	})

	if a.remoteDesktopInputStopped.Load() {
		return nil
	}

	return a.remoteDesktopInputQueue
}

func (a *Agent) remoteDesktopInputStopSignal() chan struct{} {
	if a == nil {
		return nil
	}

	a.remoteDesktopInputSignalOnce.Do(func() {
		a.remoteDesktopInputStopCh = make(chan struct{})
	})

	return a.remoteDesktopInputStopCh
}

func (a *Agent) remoteDesktopInputWorker(queue <-chan remoteDesktopInputTask, stop <-chan struct{}) {
	if queue == nil {
		return
	}

	for {
		select {
		case <-stop:
			return
		case task, ok := <-queue:
			if !ok {
				return
			}
			if task.module == nil {
				continue
			}
			if err := task.module.HandleInputBurst(task.ctx, task.burst); err != nil {
				if a.logger != nil && !errors.Is(err, context.Canceled) {
					a.logger.Printf("remote desktop input error: %v", err)
				}
			}
		}
	}
}

func (a *Agent) handleAppVncInput(ctx context.Context, burst protocol.AppVncInputBurst) {
	if a == nil || len(burst.Events) == 0 {
		return
	}
	if a.modules == nil {
		return
	}

	module := a.modules.appVncModule()
	if module == nil {
		if a.logger != nil {
			a.logger.Printf("app-vnc input ignored: module unavailable")
		}
		return
	}

	if err := module.HandleInputBurst(ctx, burst); err != nil {
		if a.logger != nil {
			a.logger.Printf("app-vnc input failed: %v", err)
		}
	}
}

func (a *Agent) stopRemoteDesktopInputWorker() {
	if a == nil {
		return
	}

	if !a.remoteDesktopInputStopped.CompareAndSwap(false, true) {
		return
	}

	stop := a.remoteDesktopInputStopSignal()
	if stop != nil {
		close(stop)
	}
}

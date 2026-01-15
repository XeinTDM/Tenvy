package agent

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mrand "math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type temporaryError interface {
	error
	Temporary() bool
}

type registrationError struct {
	err        error
	temporary  bool
	retryAfter time.Duration
}

func (e *registrationError) Error() string {
	if e == nil || e.err == nil {
		return "registration error"
	}
	return e.err.Error()
}

func (e *registrationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *registrationError) Temporary() bool {
	if e == nil {
		return false
	}
	return e.temporary
}

func (e *registrationError) RetryAfter() time.Duration {
	if e == nil {
		return 0
	}
	return e.retryAfter
}

func registerAgentWithRetry(
	ctx context.Context,
	logger *log.Logger,
	client *http.Client,
	serverURL, token string,
	metadata protocol.AgentMetadata,
	maxBackoff time.Duration,
	headers []CustomHeader,
	cookies []CustomCookie,
	userAgent string,
	disableUserAgent bool,
) (*protocol.AgentRegistrationResponse, []byte, error) {
	if maxBackoff <= 0 {
		maxBackoff = defaultBackoff
	}

	backoff := time.Second
	for attempt := 1; ; attempt++ {
		registration, sharedSecret, err := registerAgent(ctx, client, serverURL, token, metadata, headers, cookies, userAgent, disableUserAgent)
		if err == nil {
			if attempt > 1 {
				logger.Printf("registration succeeded after %d attempts", attempt)
			}
			return registration, sharedSecret, nil
		}

		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, err
		}

		if tempErr, ok := err.(temporaryError); ok && !tempErr.Temporary() {
			logger.Printf("registration aborted after %d attempts: %v", attempt, err)
			return nil, nil, err
		}

		logger.Printf("registration attempt %d failed: %v", attempt, err)

		wait := minDuration(jitterDuration(backoff), maxBackoff)
		if raErr, ok := err.(interface{ RetryAfter() time.Duration }); ok {
			if hint := raErr.RetryAfter(); hint > 0 {
				wait = minDuration(hint, maxBackoff)
			}
		}

		if wait > 0 {
			if deadline, ok := ctx.Deadline(); ok {
				remaining := time.Until(deadline)
				if remaining <= 0 {
					if err := ctx.Err(); err != nil {
						return nil, nil, err
					}
					return nil, nil, context.DeadlineExceeded
				}
				if wait > remaining {
					wait = remaining
				}
			}
		}

		if wait <= 0 {
			wait = time.Second
		}

		logger.Printf("retrying registration in %s", wait)

		if err := sleepContext(ctx, wait); err != nil {
			return nil, nil, err
		}

		backoff = minDuration(backoff*2, maxBackoff)
	}
}

func registerAgent(
	ctx context.Context,
	client *http.Client,
	serverURL, token string,
	metadata protocol.AgentMetadata,
	headers []CustomHeader,
	cookies []CustomCookie,
	userAgent string,
	disableUserAgent bool,
) (*protocol.AgentRegistrationResponse, []byte, error) {
	privKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate ecdh key: %w", err)
	}
	pubKeyHex := hex.EncodeToString(privKey.PublicKey().Bytes())

	request := protocol.AgentRegistrationRequest{
		Metadata:  metadata,
		PublicKey: &pubKeyHex,
	}
	if strings.TrimSpace(token) != "" {
		request.Token = token
	}

	data, err := json.Marshal(request)
	if err != nil {
		return nil, nil, err
	}

	endpoint := fmt.Sprintf("%s/api/agents/register", serverURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		if disableUserAgent {
			ua = ""
		} else {
			ua = resolveUserAgentString("", "", false, metadata.Version)
		}
	}
	req.Header.Set("User-Agent", ua)
	applyRequestDecorations(req, headers, cookies)

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, err
		}

		return nil, nil, &registrationError{err: err, temporary: isTemporaryNetworkError(err)}
	}
	defer resp.Body.Close()

	hint := retryAfterDuration(resp)

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return nil, nil, &registrationError{
			err:        fmt.Errorf("registration failed: %s", message),
			temporary:  isTemporaryStatus(resp.StatusCode),
			retryAfter: hint,
		}
	}

	var payload protocol.AgentRegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, nil, &registrationError{err: err, temporary: true, retryAfter: hint}
	}

	if strings.TrimSpace(payload.AgentID) == "" {
		return nil, nil, &registrationError{err: errors.New("missing agent identifier in response"), temporary: true, retryAfter: hint}
	}
	if strings.TrimSpace(payload.AgentKey) == "" {
		return nil, nil, &registrationError{err: errors.New("missing agent key in response"), temporary: true, retryAfter: hint}
	}

	var sharedSecret []byte
	if payload.ServerPublicKey != nil && *payload.ServerPublicKey != "" {
		serverPubBytes, err := hex.DecodeString(*payload.ServerPublicKey)
		if err == nil {
			serverPubKey, err := ecdh.X25519().NewPublicKey(serverPubBytes)
			if err == nil {
				sharedSecret, _ = privKey.ECDH(serverPubKey)
			}
		}
	}

	return &payload, sharedSecret, nil
}

func isTemporaryStatus(status int) bool {
	switch {
	case status >= 500:
		return true
	case status == http.StatusTooManyRequests:
		return true
	case status == http.StatusRequestTimeout:
		return true
	case status == http.StatusTooEarly:
		return true
	default:
		return false
	}
}

func retryAfterDuration(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}

	value := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}

	if when, err := http.ParseTime(value); err == nil {
		if when.IsZero() {
			return 0
		}
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}

	return 0
}

func jitterDuration(base time.Duration) time.Duration {
	if base <= 0 {
		return time.Second
	}

	const (
		minFactor = 0.8
		maxFactor = 1.3
	)

	factor := minFactor + mrand.Float64()*(maxFactor-minFactor)
	wait := time.Duration(float64(base) * factor)
	if wait <= 0 {
		return base
	}

	return wait
}

func isTemporaryNetworkError(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Timeout() {
			return true
		}

		var nestedOpErr *net.OpError
		if errors.As(urlErr.Err, &nestedOpErr) {
			return true
		}
	}

	return false
}

package clientchat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rootbay/tenvy-client/internal/protocol"
)

type Logger interface {
	Printf(format string, args ...interface{})
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Config struct {
	AgentID   string
	BaseURL   string
	AuthKey   string
	Client    HTTPDoer
	Logger    Logger
	UserAgent string
}

const (
	defaultOperatorAlias = "Operator"
	defaultClientAlias   = "Client"
	requestTimeout       = 10 * time.Second
)

type terminationReason string

const (
	reasonServerStop terminationReason = "server-stop"
	reasonCrash      terminationReason = "crash"
)

type featureFlags struct {
	allowNotifications bool
	allowFileTransfers bool
}

type OperatorMessageDelivery struct {
	SessionID string
	Message   protocol.ClientChatMessage
	Ack       func()
}

type OperatorMessageConsumer interface {
	DeliverOperatorMessage(context.Context, OperatorMessageDelivery)
}

type OperatorMessageConsumerFunc func(context.Context, OperatorMessageDelivery)

func (f OperatorMessageConsumerFunc) DeliverOperatorMessage(ctx context.Context, delivery OperatorMessageDelivery) {
	if f == nil {
		return
	}
	f(ctx, delivery)
}

type chatSession struct {
	id     string
	once   sync.Once
	notify func(terminationReason)
}

func newChatSession(id string, notify func(terminationReason)) *chatSession {
	return &chatSession{id: id, notify: notify}
}

func (s *chatSession) terminate(reason terminationReason) {
	s.once.Do(func() {
		if s.notify != nil {
			go s.notify(reason)
		}
	})
}

type Supervisor struct {
	cfg            atomic.Value
	mu             sync.Mutex
	session        *chatSession
	unstoppable    bool
	operatorAlias  string
	clientAlias    string
	features       featureFlags
	messageCounter uint64
	router         *messageRouter
}

func NewSupervisor(cfg Config) *Supervisor {
	supervisor := &Supervisor{
		operatorAlias: defaultOperatorAlias,
		clientAlias:   defaultClientAlias,
		router:        newMessageRouter(),
	}
	supervisor.updateConfig(cfg)
	return supervisor
}

func (s *Supervisor) UpdateConfig(cfg Config) {
	if s == nil {
		return
	}
	s.updateConfig(cfg)
}

func (s *Supervisor) HandleCommand(ctx context.Context, cmd protocol.Command) protocol.CommandResult {
	result := protocol.CommandResult{
		CommandID:   cmd.ID,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}

	var payload protocol.ClientChatCommandPayload
	if len(cmd.Payload) > 0 {
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			result.Error = fmt.Sprintf("invalid client chat payload: %v", err)
			return result
		}
	}

	action := strings.ToLower(strings.TrimSpace(payload.Action))
	switch action {
	case "", "start":
		sessionID, created := s.ensureSession(strings.TrimSpace(payload.SessionID))
		s.applyAliases(payload.Aliases)
		s.applyFeatures(payload.Features)
		result.Success = true
		if created {
			result.Output = fmt.Sprintf("client chat session %s started", sessionID)
		} else {
			result.Output = fmt.Sprintf("client chat session %s active", sessionID)
		}
		return result
	case "configure":
		s.applyAliases(payload.Aliases)
		s.applyFeatures(payload.Features)
		sessionID := s.currentSessionID()
		result.Success = true
		if sessionID != "" {
			result.Output = fmt.Sprintf("client chat session %s configured", sessionID)
		} else {
			result.Output = "client chat configuration updated"
		}
		return result
	case "send-message":
		sessionID, _ := s.ensureSession(strings.TrimSpace(payload.SessionID))
		s.applyAliases(payload.Aliases)
		if payload.Message == nil || strings.TrimSpace(payload.Message.Body) == "" {
			result.Error = "client chat message body is required"
			return result
		}
		envelope := s.buildOperatorEnvelope(sessionID, payload.Message)
		s.ensureRouter().enqueue(envelope)
		s.logf("client chat message for %s queued", sessionID)
		result.Success = true
		if trimmedID := strings.TrimSpace(payload.Message.ID); trimmedID != "" {
			result.Output = fmt.Sprintf("delivered chat message %s", trimmedID)
		} else {
			result.Output = fmt.Sprintf("delivered chat message to %s", sessionID)
		}
		return result
	case "stop":
		if err := s.stopSession(strings.TrimSpace(payload.SessionID)); err != nil {
			result.Error = err.Error()
			return result
		}
		result.Success = true
		result.Output = "client chat session stopped"
		return result
	default:
		result.Error = fmt.Sprintf("unsupported client chat action: %s", payload.Action)
		return result
	}
}

func (s *Supervisor) Shutdown(context.Context) {
	if s == nil {
		return
	}
	_ = s.stopSession("")
}

func (s *Supervisor) RegisterDeliveryConsumer(source string, consumer OperatorMessageConsumer) (func(), error) {
	if s == nil {
		return nil, errors.New("client chat supervisor not initialized")
	}
	return s.ensureRouter().register(source, consumer)
}

func (s *Supervisor) buildOperatorEnvelope(sessionID string, message *protocol.ClientChatCommandMessage) protocol.ClientChatMessageEnvelope {
	body := strings.TrimSpace(message.Body)
	timestamp := strings.TrimSpace(message.Timestamp)
	if timestamp == "" {
		timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	id := strings.TrimSpace(message.ID)
	if id == "" {
		id = s.nextMessageID()
	}
	alias := strings.TrimSpace(message.Alias)
	if alias == "" {
		alias = s.operatorAliasValue()
	}
	envelope := protocol.ClientChatMessageEnvelope{
		SessionID: sessionID,
		Message: protocol.ClientChatMessage{
			ID:        id,
			Body:      body,
			Timestamp: timestamp,
			Alias:     alias,
		},
	}
	return envelope
}

func (s *Supervisor) SubmitClientMessage(ctx context.Context, body string) error {
	if s == nil {
		return errors.New("client chat supervisor not initialized")
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return errors.New("client chat message cannot be empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	sessionID, _ := s.ensureSession("")
	alias := s.clientAliasValue()
	envelope := protocol.ClientChatMessageEnvelope{
		SessionID: sessionID,
		Message: protocol.ClientChatMessage{
			ID:        s.nextMessageID(),
			Body:      trimmed,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Alias:     alias,
		},
	}

	cfg := s.config()
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return errors.New("client chat: missing base URL")
	}
	if strings.TrimSpace(cfg.AgentID) == "" {
		return errors.New("client chat: missing agent identifier")
	}
	if cfg.Client == nil {
		return errors.New("client chat: missing http client")
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/api/agents/%s/chat/messages", baseURL, url.PathEscape(cfg.AgentID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if key := strings.TrimSpace(cfg.AuthKey); key != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", key))
	}

	resp, err := cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return fmt.Errorf("client chat message upload failed: %s", message)
	}

	return nil
}

func (s *Supervisor) ensureSession(sessionID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.unstoppable = true

	trimmed := strings.TrimSpace(sessionID)
	if s.session != nil {
		if trimmed == "" || trimmed == s.session.id {
			return s.session.id, false
		}
		s.session.terminate(reasonServerStop)
		s.session = s.spawnSessionLocked(trimmed)
		return trimmed, true
	}

	if trimmed == "" {
		trimmed = randomIdentifier()
	}
	s.session = s.spawnSessionLocked(trimmed)
	return trimmed, true
}

func (s *Supervisor) spawnSessionLocked(id string) *chatSession {
	session := newChatSession(id, func(reason terminationReason) {
		s.handleTermination(id, reason)
	})
	return session
}

func (s *Supervisor) handleTermination(id string, reason terminationReason) {
	s.mu.Lock()
	if s.session == nil || s.session.id != id {
		s.mu.Unlock()
		return
	}
	s.session = nil
	shouldRespawn := s.unstoppable && reason != reasonServerStop
	s.mu.Unlock()

	if shouldRespawn {
		s.logf("client chat session %s terminated (%s); respawning", id, reason)
		s.ensureSession(id)
	}
}

func (s *Supervisor) stopSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.unstoppable = false
	if s.session == nil {
		return nil
	}
	trimmed := strings.TrimSpace(sessionID)
	if trimmed != "" && trimmed != s.session.id {
		return fmt.Errorf("client chat session mismatch")
	}
	session := s.session
	s.session = nil
	session.terminate(reasonServerStop)
	return nil
}

func (s *Supervisor) currentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return ""
	}
	return s.session.id
}

func (s *Supervisor) applyAliases(aliases *protocol.ClientChatAliasConfiguration) {
	if aliases == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if trimmed := strings.TrimSpace(aliases.Operator); trimmed != "" {
		s.operatorAlias = trimmed
	}
	if trimmed := strings.TrimSpace(aliases.Client); trimmed != "" {
		s.clientAlias = trimmed
	}
}

func (s *Supervisor) applyFeatures(flags *protocol.ClientChatFeatureFlags) {
	if flags == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if flags.AllowNotifications != nil {
		s.features.allowNotifications = *flags.AllowNotifications
	}
	if flags.AllowFileTransfers != nil {
		s.features.allowFileTransfers = *flags.AllowFileTransfers
	}
	if flags.Unstoppable != nil {
		if *flags.Unstoppable {
			s.unstoppable = true
		}
	}
}

func (s *Supervisor) updateConfig(cfg Config) {
	s.cfg.Store(cfg)
}

func (s *Supervisor) config() Config {
	if value := s.cfg.Load(); value != nil {
		if cfg, ok := value.(Config); ok {
			return cfg
		}
	}
	return Config{}
}

func (s *Supervisor) logf(format string, args ...interface{}) {
	cfg := s.config()
	if cfg.Logger == nil {
		return
	}
	cfg.Logger.Printf(format, args...)
}

func (s *Supervisor) clientAliasValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.clientAlias) == "" {
		return defaultClientAlias
	}
	return s.clientAlias
}

func (s *Supervisor) ensureRouter() *messageRouter {
	s.mu.Lock()
	if s.router == nil {
		s.router = newMessageRouter()
	}
	router := s.router
	s.mu.Unlock()
	return router
}

func (s *Supervisor) operatorAliasValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.operatorAlias) == "" {
		return defaultOperatorAlias
	}
	return s.operatorAlias
}

func (s *Supervisor) nextMessageID() string {
	s.mu.Lock()
	s.messageCounter++
	counter := s.messageCounter
	s.mu.Unlock()

	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("chat-%d", counter)
}

func randomIdentifier() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return fmt.Sprintf("chat-%d", time.Now().UnixNano())
}

type messageRouter struct {
	mu        sync.Mutex
	pending   []*queuedMessage
	byID      map[string]*queuedMessage
	consumers map[string]OperatorMessageConsumer
}

type queuedMessage struct {
	envelope protocol.ClientChatMessageEnvelope
	once     sync.Once
}

func newMessageRouter() *messageRouter {
	return &messageRouter{}
}

func (r *messageRouter) register(source string, consumer OperatorMessageConsumer) (func(), error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil, errors.New("delivery consumer source required")
	}
	if consumer == nil {
		return nil, errors.New("delivery consumer cannot be nil")
	}

	r.mu.Lock()
	if r.consumers == nil {
		r.consumers = make(map[string]OperatorMessageConsumer)
	}
	r.consumers[trimmed] = consumer
	pending := r.pendingSnapshotLocked()
	r.mu.Unlock()

	if len(pending) > 0 {
		go r.dispatchToConsumer(consumer, pending)
	}

	return func() {
		r.unregister(trimmed)
	}, nil
}

func (r *messageRouter) unregister(source string) {
	trimmed := strings.TrimSpace(source)
	r.mu.Lock()
	if len(r.consumers) == 0 {
		r.mu.Unlock()
		return
	}
	if trimmed == "" {
		r.consumers = nil
		r.mu.Unlock()
		return
	}
	delete(r.consumers, trimmed)
	if len(r.consumers) == 0 {
		r.consumers = nil
	}
	r.mu.Unlock()
}

func (r *messageRouter) enqueue(envelope protocol.ClientChatMessageEnvelope) {
	r.mu.Lock()
	if r.byID == nil {
		r.byID = make(map[string]*queuedMessage)
	}
	id := strings.TrimSpace(envelope.Message.ID)
	var message *queuedMessage
	if existing, ok := r.byID[id]; ok {
		existing.envelope = envelope
		message = existing
	} else {
		message = &queuedMessage{envelope: envelope}
		r.byID[id] = message
		r.pending = append(r.pending, message)
	}
	consumers := r.consumerSnapshotLocked()
	r.mu.Unlock()

	if len(consumers) == 0 {
		return
	}

	go r.dispatch(consumers, []*queuedMessage{message})
}

func (r *messageRouter) dispatch(consumers map[string]OperatorMessageConsumer, messages []*queuedMessage) {
	if len(consumers) == 0 || len(messages) == 0 {
		return
	}
	for _, consumer := range consumers {
		consumer := consumer
		go r.dispatchToConsumer(consumer, messages)
	}
}

func (r *messageRouter) dispatchToConsumer(consumer OperatorMessageConsumer, messages []*queuedMessage) {
	if consumer == nil {
		return
	}
	ctx := context.Background()
	for _, message := range messages {
		delivery := OperatorMessageDelivery{
			SessionID: message.envelope.SessionID,
			Message:   message.envelope.Message,
			Ack:       message.ackFunc(r),
		}
		consumer.DeliverOperatorMessage(ctx, delivery)
	}
}

func (q *queuedMessage) ackFunc(router *messageRouter) func() {
	if q == nil {
		return func() {}
	}
	return func() {
		q.once.Do(func() {
			router.acknowledge(q)
		})
	}
}

func (r *messageRouter) acknowledge(message *queuedMessage) {
	if message == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := strings.TrimSpace(message.envelope.Message.ID)
	if id == "" {
		return
	}
	if existing, ok := r.byID[id]; !ok || existing != message {
		return
	}
	delete(r.byID, id)
	for i, candidate := range r.pending {
		if candidate == message {
			r.pending = append(r.pending[:i], r.pending[i+1:]...)
			break
		}
	}
}

func (r *messageRouter) pendingSnapshotLocked() []*queuedMessage {
	if len(r.pending) == 0 {
		return nil
	}
	snapshot := make([]*queuedMessage, len(r.pending))
	copy(snapshot, r.pending)
	return snapshot
}

func (r *messageRouter) consumerSnapshotLocked() map[string]OperatorMessageConsumer {
	if len(r.consumers) == 0 {
		return nil
	}
	snapshot := make(map[string]OperatorMessageConsumer, len(r.consumers))
	for key, consumer := range r.consumers {
		snapshot[key] = consumer
	}
	return snapshot
}

func (r *messageRouter) pendingLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

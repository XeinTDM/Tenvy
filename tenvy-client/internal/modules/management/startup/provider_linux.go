//go:build linux

package startup

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const managedCommentPrefix = "# tenvy-managed"

type cronCommandRunner interface {
	run(ctx context.Context, name string, args ...string) ([]byte, error)
	runWithInput(ctx context.Context, input string, name string, args ...string) ([]byte, error)
}

type execCronRunner struct{}

func (execCronRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return output, fmt.Errorf("%w: %s", err, trimmed)
		}
		return output, err
	}
	return output, nil
}

func (execCronRunner) runWithInput(ctx context.Context, input string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return output, fmt.Errorf("%w: %s", err, trimmed)
		}
		return output, err
	}
	return output, nil
}

type nativeProvider struct {
	cronPath string
	runner   cronCommandRunner
	caps     ProviderCapabilities
}

func newNativeProvider() Provider {
	path, _ := exec.LookPath("crontab")
	caps := ProviderCapabilities{}
	if path != "" {
		caps = ProviderCapabilities{Enumerate: true, Manage: true}
	}
	return &nativeProvider{cronPath: path, runner: execCronRunner{}, caps: caps}
}

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return p.caps
}

func (p *nativeProvider) ensureAvailable() error {
	if p.cronPath == "" {
		return ErrNotSupported
	}
	if p.runner == nil {
		p.runner = execCronRunner{}
	}
	return nil
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (Inventory, error) {
	if err := p.ensureAvailable(); err != nil {
		return Inventory{}, err
	}

	state, err := p.fetchState(ctx)
	if err != nil {
		return Inventory{}, err
	}

	entries := make([]Entry, 0, len(state.records))
	summary := &TelemetrySummary{ImpactCounts: map[StartupImpact]int{}, ScopeCounts: map[StartupScope]int{}}
	for _, record := range state.records {
		entry := record.toEntry()
		entries = append(entries, entry)
		summary.Total++
		if entry.Enabled {
			summary.Enabled++
		} else {
			summary.Disabled++
		}
		summary.ScopeCounts[entry.Scope]++
		summary.ImpactCounts[entry.Impact]++
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return Inventory{
		Entries:     entries,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Summary:     summary,
	}, nil
}

func (p *nativeProvider) Toggle(ctx context.Context, req ToggleRequest) (Entry, error) {
	if err := p.ensureAvailable(); err != nil {
		return Entry{}, err
	}
	state, err := p.fetchState(ctx)
	if err != nil {
		return Entry{}, err
	}
	record, ok := state.records[req.EntryID]
	if !ok {
		return Entry{}, fmt.Errorf("startup entry %s not found", req.EntryID)
	}
	if !record.managed {
		return Entry{}, fmt.Errorf("startup entry %s is not managed by tenvy", req.EntryID)
	}
	if record.enabled == req.Enabled {
		return record.toEntry(), nil
	}
	if err := state.toggle(record, req.Enabled); err != nil {
		return Entry{}, err
	}
	if err := p.applyState(ctx, state); err != nil {
		return Entry{}, err
	}
	record.enabled = req.Enabled
	entry := record.toEntry()
	entry.Enabled = req.Enabled
	return entry, nil
}

func (p *nativeProvider) Create(ctx context.Context, req CreateRequest) (Entry, error) {
	if err := p.ensureAvailable(); err != nil {
		return Entry{}, err
	}
	definition := req.Definition
	if strings.TrimSpace(definition.Path) == "" {
		return Entry{}, fmt.Errorf("startup definition path required")
	}
	state, err := p.fetchState(ctx)
	if err != nil {
		return Entry{}, err
	}
	record := state.appendManaged(definition)
	if err := p.applyState(ctx, state); err != nil {
		return Entry{}, err
	}
	return record.toEntry(), nil
}

func (p *nativeProvider) Remove(ctx context.Context, req RemoveRequest) (RemoveResult, error) {
	if err := p.ensureAvailable(); err != nil {
		return RemoveResult{}, err
	}
	state, err := p.fetchState(ctx)
	if err != nil {
		return RemoveResult{}, err
	}
	record, ok := state.records[req.EntryID]
	if !ok {
		return RemoveResult{}, fmt.Errorf("startup entry %s not found", req.EntryID)
	}
	if !record.managed {
		return RemoveResult{}, fmt.Errorf("startup entry %s is not managed by tenvy", req.EntryID)
	}
	if err := state.remove(record); err != nil {
		return RemoveResult{}, err
	}
	if err := p.applyState(ctx, state); err != nil {
		return RemoveResult{}, err
	}
	return RemoveResult{EntryID: req.EntryID}, nil
}

func (p *nativeProvider) fetchState(ctx context.Context) (*cronState, error) {
	output, err := p.runner.run(ctx, p.cronPath, "-l")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no crontab") {
			return newCronState(""), nil
		}
		return nil, err
	}
	return newCronState(string(output)), nil
}

func (p *nativeProvider) applyState(ctx context.Context, state *cronState) error {
	_, err := p.runner.runWithInput(ctx, state.serialize(), p.cronPath, "-")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no crontab") {
			_, retryErr := p.runner.runWithInput(ctx, state.serialize(), p.cronPath, "-")
			if retryErr == nil {
				return nil
			}
			return retryErr
		}
	}
	return err
}

type cronState struct {
	lines   []string
	records map[string]*cronRecord
}

type cronRecord struct {
	id           string
	managed      bool
	commentIndex int
	commandIndex int
	schedule     string
	commandLine  string
	metadata     map[string]string
	enabled      bool
}

func newCronState(raw string) *cronState {
	if raw == "" {
		return &cronState{lines: []string{}, records: make(map[string]*cronRecord)}
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	state := &cronState{lines: lines, records: make(map[string]*cronRecord)}
	state.parse()
	return state
}

func (s *cronState) parse() {
	s.records = make(map[string]*cronRecord)
	for idx := 0; idx < len(s.lines); idx++ {
		line := strings.TrimSpace(s.lines[idx])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, managedCommentPrefix) {
			metadata := parseMetadata(line[len(managedCommentPrefix):])
			for j := idx + 1; j < len(s.lines); j++ {
				commandRaw := s.lines[j]
				trimmed := strings.TrimSpace(commandRaw)
				if trimmed == "" {
					continue
				}
				record := newCronRecord(metadata, idx, j, commandRaw)
				s.records[record.id] = record
				idx = j
				break
			}
			continue
		}

		trimmed := strings.TrimSpace(s.lines[idx])
		if strings.HasPrefix(trimmed, "@reboot") || strings.HasPrefix(trimmed, "#@reboot") {
			record := newCronRecord(nil, -1, idx, s.lines[idx])
			s.records[record.id] = record
		}
	}
}

func (s *cronState) serialize() string {
	if len(s.lines) == 0 {
		return "\n"
	}
	return strings.Join(s.lines, "\n") + "\n"
}

func (s *cronState) appendManaged(def EntryDefinition) *cronRecord {
	id := strings.TrimSpace(def.Name)
	if id == "" {
		id = fmt.Sprintf("tenvy-%d", time.Now().UnixNano())
	}
	if _, exists := s.records[id]; exists {
		id = fmt.Sprintf("%s-%d", id, len(s.records)+1)
	}
	metadata := map[string]string{
		"id":    id,
		"name":  def.Name,
		"scope": string(def.Scope),
	}
	comment := buildMetadataComment(metadata)
	command := buildCronCommand(def.Path, def.Arguments, def.Enabled)
	s.lines = append(s.lines, comment, command)
	record := newCronRecord(metadata, len(s.lines)-2, len(s.lines)-1, command)
	record.managed = true
	record.enabled = def.Enabled
	record.metadata = metadata
	s.records[id] = record
	return record
}

func (s *cronState) toggle(record *cronRecord, enabled bool) error {
	if record.commandIndex < 0 || record.commandIndex >= len(s.lines) {
		return errors.New("invalid cron record index")
	}
	current := s.lines[record.commandIndex]
	trimmed := strings.TrimLeft(current, " \t")
	if enabled {
		if strings.HasPrefix(trimmed, "#") {
			trimmed = strings.TrimPrefix(trimmed, "#")
			trimmed = strings.TrimSpace(trimmed)
			s.lines[record.commandIndex] = "@" + strings.TrimPrefix(trimmed, "@")
		}
	} else {
		if !strings.HasPrefix(trimmed, "#") {
			s.lines[record.commandIndex] = "#" + trimmed
		}
	}
	record.enabled = enabled
	return nil
}

func (s *cronState) remove(record *cronRecord) error {
	newLines := make([]string, 0, len(s.lines))
	for idx, line := range s.lines {
		if idx == record.commentIndex || idx == record.commandIndex {
			continue
		}
		newLines = append(newLines, line)
	}
	s.lines = newLines
	delete(s.records, record.id)
	s.parse()
	return nil
}

func newCronRecord(metadata map[string]string, commentIndex, commandIndex int, commandRaw string) *cronRecord {
	record := &cronRecord{
		commentIndex: commentIndex,
		commandIndex: commandIndex,
		metadata:     make(map[string]string),
	}
	if metadata != nil {
		for k, v := range metadata {
			record.metadata[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	record.commandLine = strings.TrimSpace(commandRaw)
	record.schedule = "@reboot"
	trimmed := strings.TrimLeft(commandRaw, " \t")
	record.enabled = !strings.HasPrefix(trimmed, "#")
	cleaned := strings.TrimPrefix(trimmed, "#")
	cleaned = strings.TrimSpace(cleaned)
	record.commandLine = cleaned

	if id, ok := record.metadata["id"]; ok && id != "" {
		record.id = id
		record.managed = true
	} else {
		record.id = hashCommand(cleaned)
	}
	return record
}

func (r *cronRecord) toEntry() Entry {
	fields := strings.Fields(r.commandLine)
	path := ""
	args := ""
	if len(fields) > 0 {
		path = fields[0]
		if len(fields) > 1 {
			args = strings.Join(fields[1:], " ")
		}
	}
	name := r.metadata["name"]
	if name == "" {
		name = path
	}
	scope := ScopeUser
	if raw := r.metadata["scope"]; raw != "" {
		scope = StartupScope(raw)
	}
	return Entry{
		ID:              r.id,
		Name:            name,
		Path:            path,
		Arguments:       args,
		Enabled:         r.enabled,
		Scope:           scope,
		Source:          SourceScheduledTask,
		Impact:          ImpactMedium,
		Location:        "crontab (@reboot)",
		StartupTime:     0,
		LastEvaluatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Metadata: map[string]interface{}{
			"managed": r.managed,
			"raw":     r.commandLine,
		},
	}
}

func parseMetadata(raw string) map[string]string {
	result := make(map[string]string)
	fields := strings.Fields(strings.TrimSpace(raw))
	for _, field := range fields {
		if strings.Contains(field, "=") {
			parts := strings.SplitN(field, "=", 2)
			key := strings.TrimSpace(parts[0])
			value := ""
			if len(parts) > 1 {
				value = strings.Trim(strings.TrimSpace(parts[1]), "\"")
			}
			result[key] = value
		}
	}
	return result
}

func buildMetadataComment(metadata map[string]string) string {
	parts := []string{managedCommentPrefix}
	for key, value := range metadata {
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(parts, " ")
}

func buildCronCommand(path, args string, enabled bool) string {
	command := strings.TrimSpace(path)
	if args = strings.TrimSpace(args); args != "" {
		command = command + " " + args
	}
	line := fmt.Sprintf("@reboot %s", command)
	if !enabled {
		return "#" + line
	}
	return line
}

func hashCommand(command string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(command)))
	return "cron-" + hex.EncodeToString(sum[:8])
}

func newTestProviderWithRunner(r cronCommandRunner) *nativeProvider {
	provider := newNativeProvider().(*nativeProvider)
	provider.runner = r
	if provider.cronPath == "" {
		provider.cronPath = "crontab"
	}
	provider.caps = ProviderCapabilities{Enumerate: true, Manage: true}
	return provider
}

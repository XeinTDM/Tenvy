//go:build darwin

package startupengine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	plist "howett.net/plist"
)

const launchAgentPrefix = "com.tenvy."

type nativeProvider struct {
	launchctl string
	runner    launchdRunner
	baseDir   string
	caps      ProviderCapabilities
}

type launchdRunner interface {
	run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execLaunchdRunner struct{}

func (execLaunchdRunner) run(ctx context.Context, name string, args ...string) ([]byte, error) {
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

func newNativeProvider() Provider {
	path, _ := exec.LookPath("launchctl")
	caps := ProviderCapabilities{}
	if path != "" {
		caps = ProviderCapabilities{Enumerate: true, Manage: true}
	}
	home, err := os.UserHomeDir()
	base := ""
	if err == nil {
		base = filepath.Join(home, "Library", "LaunchAgents")
	}
	return &nativeProvider{
		launchctl: path,
		runner:    execLaunchdRunner{},
		baseDir:   base,
		caps:      caps,
	}
}

func (p *nativeProvider) Capabilities() ProviderCapabilities {
	return p.caps
}

func (p *nativeProvider) ensureAvailable() error {
	if p.launchctl == "" {
		return ErrNotSupported
	}
	if p.runner == nil {
		p.runner = execLaunchdRunner{}
	}
	if strings.TrimSpace(p.baseDir) == "" {
		return errors.New("launch agents directory unavailable")
	}
	return nil
}

func (p *nativeProvider) List(ctx context.Context, req ListRequest) (Inventory, error) {
	if err := p.ensureAvailable(); err != nil {
		return Inventory{}, err
	}
	entries := make([]Entry, 0, 8)
	dirEntries, err := os.ReadDir(p.baseDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Inventory{Entries: nil, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
		}
		return Inventory{}, err
	}
	for _, entry := range dirEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".plist") {
			continue
		}
		agent, err := p.readAgent(filepath.Join(p.baseDir, entry.Name()))
		if err != nil {
			continue
		}
		entries = append(entries, agent)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	summary := &TelemetrySummary{ImpactCounts: map[StartupImpact]int{}, ScopeCounts: map[StartupScope]int{}}
	for _, entry := range entries {
		summary.Total++
		if entry.Enabled {
			summary.Enabled++
		} else {
			summary.Disabled++
		}
		summary.ScopeCounts[entry.Scope]++
		summary.ImpactCounts[entry.Impact]++
	}
	return Inventory{Entries: entries, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano), Summary: summary}, nil
}

func (p *nativeProvider) Toggle(ctx context.Context, req ToggleRequest) (Entry, error) {
	if err := p.ensureAvailable(); err != nil {
		return Entry{}, err
	}
	path := p.agentPath(req.EntryID)
	agent, err := p.readAgent(path)
	if err != nil {
		return Entry{}, err
	}
	managed := strings.HasPrefix(agent.ID, launchAgentPrefix)
	if !managed {
		return Entry{}, fmt.Errorf("startup entry %s is not managed by tenvy", req.EntryID)
	}
	if agent.Enabled == req.Enabled {
		return agent, nil
	}
	if req.Enabled {
		if _, err := p.runner.run(ctx, p.launchctl, "load", path); err != nil {
			return Entry{}, err
		}
	} else {
		if _, err := p.runner.run(ctx, p.launchctl, "unload", path); err != nil {
			return Entry{}, err
		}
	}
	agent.Enabled = req.Enabled
	if err := p.writeAgent(agent); err != nil {
		return Entry{}, err
	}
	return agent, nil
}

func (p *nativeProvider) Create(ctx context.Context, req CreateRequest) (Entry, error) {
	if err := p.ensureAvailable(); err != nil {
		return Entry{}, err
	}
	def := req.Definition
	if strings.TrimSpace(def.Path) == "" {
		return Entry{}, fmt.Errorf("startup definition path required")
	}
	if err := os.MkdirAll(p.baseDir, 0o755); err != nil {
		return Entry{}, err
	}
	label := fmt.Sprintf("%s%s", launchAgentPrefix, sanitizeLabel(def.Name))
	if label == launchAgentPrefix {
		label = fmt.Sprintf("%s%x", launchAgentPrefix, time.Now().UnixNano())
	}
	agent := Entry{
		ID:              label,
		Name:            def.Name,
		Path:            strings.TrimSpace(def.Path),
		Arguments:       strings.TrimSpace(def.Arguments),
		Enabled:         def.Enabled,
		Scope:           ScopeUser,
		Source:          SourceService,
		Impact:          ImpactMedium,
		Location:        filepath.Join(p.baseDir, fmt.Sprintf("%s.plist", label)),
		StartupTime:     0,
		LastEvaluatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Metadata: map[string]interface{}{
			"managed": true,
			"label":   label,
		},
	}
	if err := p.writeAgent(agent); err != nil {
		return Entry{}, err
	}
	if agent.Enabled {
		if _, err := p.runner.run(ctx, p.launchctl, "load", agent.Location); err != nil {
			return Entry{}, err
		}
	}
	return agent, nil
}

func (p *nativeProvider) Remove(ctx context.Context, req RemoveRequest) (RemoveResult, error) {
	if err := p.ensureAvailable(); err != nil {
		return RemoveResult{}, err
	}
	path := p.agentPath(req.EntryID)
	if _, err := os.Stat(path); err != nil {
		return RemoveResult{}, err
	}
	_, _ = p.runner.run(ctx, p.launchctl, "unload", path)
	if err := os.Remove(path); err != nil {
		return RemoveResult{}, err
	}
	return RemoveResult{EntryID: req.EntryID}, nil
}

func (p *nativeProvider) agentPath(label string) string {
	fileName := label
	if !strings.HasSuffix(fileName, ".plist") {
		fileName = fileName + ".plist"
	}
	return filepath.Join(p.baseDir, fileName)
}

func (p *nativeProvider) readAgent(path string) (Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, err
	}
	var payload map[string]interface{}
	if _, err := plist.Unmarshal(data, &payload); err != nil {
		return Entry{}, err
	}
	label, _ := payload["Label"].(string)
	program, _ := payload["Program"].(string)
	argsAny, _ := payload["ProgramArguments"].([]interface{})
	args := []string{}
	for _, value := range argsAny {
		if s, ok := value.(string); ok {
			args = append(args, s)
		}
	}
	if program == "" && len(args) > 0 {
		program = args[0]
		args = args[1:]
	}
	disabled := false
	if v, ok := payload["Disabled"].(bool); ok {
		disabled = v
	}
	name := label
	if metaName, ok := payload["TenvyName"].(string); ok && metaName != "" {
		name = metaName
	}
	return Entry{
		ID:              label,
		Name:            name,
		Path:            program,
		Arguments:       strings.Join(args, " "),
		Enabled:         !disabled,
		Scope:           ScopeUser,
		Source:          SourceService,
		Impact:          ImpactMedium,
		Location:        path,
		StartupTime:     0,
		LastEvaluatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Metadata: map[string]interface{}{
			"managed": strings.HasPrefix(label, launchAgentPrefix),
			"label":   label,
		},
	}, nil
}

func (p *nativeProvider) writeAgent(entry Entry) error {
	program := strings.TrimSpace(entry.Path)
	args := []string{}
	if entry.Arguments != "" {
		args = append([]string{program}, strings.Fields(entry.Arguments)...)
	}
	payload := map[string]interface{}{
		"Label":            entry.ID,
		"Program":          program,
		"ProgramArguments": args,
		"RunAtLoad":        true,
		"Disabled":         !entry.Enabled,
		"TenvyName":        entry.Name,
	}
	data, err := plist.MarshalIndent(payload, plist.XMLFormat, "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(entry.Location, data, 0o644); err != nil {
		return err
	}
	return nil
}

func sanitizeLabel(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	sanitized := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r
		}
		if r >= '0' && r <= '9' {
			return r
		}
		if r == '.' || r == '-' {
			return r
		}
		return '-'
	}, trimmed)
	return strings.Trim(sanitized, "-.")
}

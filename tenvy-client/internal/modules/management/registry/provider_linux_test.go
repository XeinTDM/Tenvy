//go:build linux

package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	mu       sync.Mutex
	commands []string
	outputs  map[string][]byte
	errors   map[string]error
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		outputs: make(map[string][]byte),
		errors:  make(map[string]error),
	}
}

func (f *fakeRunner) set(command string, output string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputs[command] = []byte(output)
	if err != nil {
		f.errors[command] = err
	} else {
		delete(f.errors, command)
	}
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	cmd := strings.TrimSpace(strings.Join(append([]string{name}, args...), " "))
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	output := f.outputs[cmd]
	err := f.errors[cmd]
	f.mu.Unlock()
	return output, err
}

func (f *fakeRunner) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func TestLinuxProviderListEnumeratesSchemas(t *testing.T) {
	runner := newFakeRunner()
	provider := newTestNativeProvider(runner)
	prefix := provider.gsettingsPath
	runner.set(fmt.Sprintf("%s list-schemas", prefix), "org.example.alpha\norg.example.beta\n", nil)
	runner.set(fmt.Sprintf("%s list-recursively org.example.alpha", prefix), "org.example.alpha first-key 'value'\n", nil)
	runner.set(fmt.Sprintf("%s list-recursively org.example.beta", prefix), "org.example.beta flag true\n", nil)
	if provider.Capabilities().Enumerate != true {
		t.Fatalf("expected enumeration capability enabled")
	}

	result, err := provider.List(context.Background(), ListRequest{})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	hive, ok := result.Snapshot[gsettingsHiveName]
	if !ok {
		t.Fatalf("snapshot missing gsettings hive: %+v", result.Snapshot)
	}
	if len(hive) != 2 {
		t.Fatalf("expected two schemas, got %d", len(hive))
	}
	alpha := hive["org.example.alpha"]
	if len(alpha.Values) != 1 || alpha.Values[0].Name != "first-key" {
		t.Fatalf("unexpected alpha schema data: %+v", alpha)
	}
	beta := hive["org.example.beta"]
	if len(beta.Values) != 1 || beta.Values[0].Type != "bool" {
		t.Fatalf("unexpected beta schema data: %+v", beta)
	}
}

func TestLinuxProviderWriteValue(t *testing.T) {
	runner := newFakeRunner()
	provider := newTestNativeProvider(runner)
	prefix := provider.gsettingsPath
	runner.set(fmt.Sprintf("%s list-recursively org.example.alpha", prefix), "org.example.alpha first-key 'value'\n", nil)

	mutation, err := provider.CreateValue(context.Background(), CreateValueRequest{
		Hive:    gsettingsHiveName,
		KeyPath: "org.example.alpha",
		Value: RegistryValueInput{
			Name: "first-key",
			Type: "string",
			Data: "updated",
		},
	})
	if err != nil {
		t.Fatalf("create value failed: %v", err)
	}
	if mutation.KeyPath != "org.example.alpha" {
		t.Fatalf("unexpected key path %q", mutation.KeyPath)
	}
	calls := runner.calls()
	if len(calls) == 0 {
		t.Fatalf("expected gsettings invocation")
	}
	expected := fmt.Sprintf("%s set org.example.alpha first-key 'updated'", prefix)
	found := false
	for _, call := range calls {
		if call == expected {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected command %q in calls: %v", expected, calls)
	}
}

func TestLinuxProviderDeleteValueHandlesMissingSchema(t *testing.T) {
	runner := newFakeRunner()
	provider := newTestNativeProvider(runner)

	_, err := provider.DeleteValue(context.Background(), DeleteValueRequest{Hive: "", KeyPath: "", Name: "example"})
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if !strings.Contains(err.Error(), "schema") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLinuxProviderReturnsMutationSnapshot(t *testing.T) {
	runner := newFakeRunner()
	runner.set("gsettings reset org.example.alpha first-key", "", nil)
	runner.set("gsettings list-recursively org.example.alpha", "", nil)
	provider := newTestNativeProvider(runner)

	mutation, err := provider.DeleteValue(context.Background(), DeleteValueRequest{
		Hive:    gsettingsHiveName,
		KeyPath: "org.example.alpha",
		Name:    "first-key",
	})
	if err != nil {
		t.Fatalf("delete value failed: %v", err)
	}
	if mutation.ValueName == nil || *mutation.ValueName != "first-key" {
		t.Fatalf("unexpected mutation response: %+v", mutation)
	}
	if _, ok := mutation.Hive["org.example.alpha"]; !ok {
		t.Fatalf("expected hive snapshot to include schema: %+v", mutation.Hive)
	}
}

func TestLinuxProviderRespectsContextCancellation(t *testing.T) {
	runner := newFakeRunner()
	runner.set("gsettings list-schemas", "", context.DeadlineExceeded)
	provider := newTestNativeProvider(runner)

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	if _, err := provider.List(ctx, ListRequest{}); err == nil {
		t.Fatalf("expected list cancellation error")
	}
}

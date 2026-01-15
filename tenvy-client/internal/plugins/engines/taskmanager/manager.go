package taskmanagerengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	gproc "github.com/shirou/gopsutil/v3/process"

	"github.com/rootbay/tenvy-client/internal/platform"
	"github.com/rootbay/tenvy-client/internal/protocol"
)

type (
	Command       = protocol.Command
	CommandResult = protocol.CommandResult
)

type Logger interface {
	Printf(format string, args ...interface{})
}

type Manager struct {
	logger Logger
}

type ProcessStatus string

type ProcessAction string

type TaskManagerOperation string

const (
	ProcessStatusRunning   ProcessStatus = "running"
	ProcessStatusSleeping  ProcessStatus = "sleeping"
	ProcessStatusStopped   ProcessStatus = "stopped"
	ProcessStatusIdle      ProcessStatus = "idle"
	ProcessStatusZombie    ProcessStatus = "zombie"
	ProcessStatusSuspended ProcessStatus = "suspended"
	ProcessStatusUnknown   ProcessStatus = "unknown"

	ProcessActionStop      ProcessAction = "stop"
	ProcessActionForceStop ProcessAction = "force-stop"
	ProcessActionSuspend   ProcessAction = "suspend"
	ProcessActionResume    ProcessAction = "resume"
	ProcessActionRestart   ProcessAction = "restart"

	OperationList   TaskManagerOperation = "list"
	OperationDetail TaskManagerOperation = "detail"
	OperationStart  TaskManagerOperation = "start"
	OperationAction TaskManagerOperation = "action"
)

type ProcessSummary struct {
	PID       int32         `json:"pid"`
	PPID      int32         `json:"ppid,omitempty"`
	Name      string        `json:"name"`
	CPU       float64       `json:"cpu"`
	Memory    uint64        `json:"memory"`
	Status    ProcessStatus `json:"status"`
	Command   string        `json:"command"`
	User      string        `json:"user,omitempty"`
	StartedAt string        `json:"startedAt,omitempty"`
}

type ProcessDetail struct {
	ProcessSummary
	Path      string   `json:"path,omitempty"`
	Arguments []string `json:"arguments,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Threads   int32    `json:"threads,omitempty"`
	Priority  *int32   `json:"priority,omitempty"`
	Nice      *int32   `json:"nice,omitempty"`
	CPUTime   float64  `json:"cpuTime,omitempty"`
}

type ProcessListResponse struct {
	Processes   []ProcessSummary `json:"processes"`
	GeneratedAt string           `json:"generatedAt"`
}

type StartProcessRequest struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type StartProcessResponse struct {
	PID       int      `json:"pid"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	StartedAt string   `json:"startedAt"`
}

type ProcessActionRequest struct {
	Action ProcessAction `json:"action"`
}

type ProcessActionResponse struct {
	PID     int           `json:"pid"`
	Action  ProcessAction `json:"action"`
	Status  string        `json:"status"`
	Message string        `json:"message,omitempty"`
}

type TaskManagerCommandRequest struct {
	Operation TaskManagerOperation `json:"operation"`
	PID       int32                `json:"pid,omitempty"`
	Payload   *StartProcessRequest `json:"payload,omitempty"`
	Action    ProcessAction        `json:"action,omitempty"`
}

type TaskManagerCommandPayload struct {
	Request TaskManagerCommandRequest `json:"request"`
}

type TaskManagerCommandResponse struct {
	Operation TaskManagerOperation `json:"operation"`
	Status    string               `json:"status"`
	Result    interface{}          `json:"result,omitempty"`
	Error     string               `json:"error,omitempty"`
	Code      string               `json:"code,omitempty"`
	Details   interface{}          `json:"details,omitempty"`
}

func NewManager(logger Logger) *Manager {
	return &Manager{logger: logger}
}

func (m *Manager) UpdateLogger(logger Logger) {
	m.logger = logger
}

func (m *Manager) logf(format string, args ...interface{}) {
	if m.logger == nil {
		return
	}
	m.logger.Printf(format, args...)
}

func (m *Manager) HandleCommand(ctx context.Context, cmd Command) CommandResult {
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)
	result := CommandResult{CommandID: cmd.ID, CompletedAt: completedAt}

	if len(cmd.Payload) == 0 {
		result.Success = false
		result.Error = "task manager payload required"
		return result
	}

	var payload TaskManagerCommandPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("invalid task manager payload: %v", err)
		return result
	}

	request := payload.Request
	switch request.Operation {
	case OperationList:
		response, err := m.listProcesses(ctx)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, OperationList, response); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	case OperationDetail:
		if request.PID <= 0 {
			result.Success = false
			result.Error = "process identifier is required"
			return result
		}
		response, err := m.describeProcess(ctx, request.PID)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, OperationDetail, response); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	case OperationStart:
		if request.Payload == nil {
			result.Success = false
			result.Error = "start process payload missing"
			return result
		}
		response, err := m.startProcess(ctx, *request.Payload)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, OperationStart, response); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	case OperationAction:
		if request.PID <= 0 {
			result.Success = false
			result.Error = "process identifier is required"
			return result
		}
		if request.Action == "" {
			result.Success = false
			result.Error = "process action is required"
			return result
		}
		response, err := m.performAction(ctx, request.PID, request.Action)
		if err != nil {
			result.Success = false
			result.Error = err.Error()
			return result
		}
		if err := m.setSuccessResult(&result, OperationAction, response); err != nil {
			result.Success = false
			result.Error = fmt.Sprintf("encode response: %v", err)
			return result
		}
		result.Success = true
		return result
	default:
		result.Success = false
		result.Error = fmt.Sprintf("unsupported task manager operation: %s", request.Operation)
		return result
	}
}

func (m *Manager) setSuccessResult(result *CommandResult, operation TaskManagerOperation, payload interface{}) error {
	response := TaskManagerCommandResponse{
		Operation: operation,
		Status:    "ok",
		Result:    payload,
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	result.Output = string(data)
	return nil
}

func (m *Manager) listProcesses(ctx context.Context) (*ProcessListResponse, error) {
	processes, err := gproc.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("enumerate processes: %w", err)
	}

	summaries := make([]ProcessSummary, 0, len(processes))
	for _, proc := range processes {
		summary, err := m.buildSummary(ctx, proc)
		if err != nil {
			m.logf("task-manager: failed to build summary for pid %d: %v", proc.Pid, err)
			continue
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].PID < summaries[j].PID
	})

	return &ProcessListResponse{
		Processes:   summaries,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (m *Manager) describeProcess(ctx context.Context, pid int32) (*ProcessDetail, error) {
	proc, err := gproc.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}

	summary, err := m.buildSummary(ctx, proc)
	if err != nil {
		return nil, err
	}

	detail := &ProcessDetail{ProcessSummary: summary}

	if exe, err := proc.ExeWithContext(ctx); err == nil && exe != "" {
		detail.Path = exe
	}

	if args, err := proc.CmdlineSliceWithContext(ctx); err == nil {
		if len(args) > 0 {
			detail.Command = strings.Join(args, " ")
			if len(args) > 1 {
				detail.Arguments = append([]string(nil), args[1:]...)
			}
		}
	}

	if cwd, err := proc.CwdWithContext(ctx); err == nil && cwd != "" {
		detail.Cwd = cwd
	}

	if threads, err := proc.NumThreadsWithContext(ctx); err == nil && threads > 0 {
		detail.Threads = threads
	}

	if nice, err := proc.NiceWithContext(ctx); err == nil {
		detail.Nice = pointer(int32(nice))
	}

	if times, err := proc.TimesWithContext(ctx); err == nil && times != nil {
		cpuTime := times.User + times.System
		if cpuTime > 0 {
			detail.CPUTime = round(cpuTime, 3)
		}
	}

	return detail, nil
}

func (m *Manager) startProcess(ctx context.Context, payload StartProcessRequest) (*StartProcessResponse, error) {
	command := strings.TrimSpace(payload.Command)
	if command == "" {
		return nil, errors.New("command is required")
	}

	args := sanitizeArgs(payload.Args)
	cmd := exec.CommandContext(ctx, command, args...)
	if payload.Cwd != "" {
		cmd.Dir = payload.Cwd
	}

	if len(payload.Env) > 0 {
		cmd.Env = mergeEnv(os.Environ(), payload.Env)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process: %w", err)
	}

	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	return &StartProcessResponse{
		PID:       pid,
		Command:   command,
		Args:      args,
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (m *Manager) performAction(ctx context.Context, pid int32, action ProcessAction) (*ProcessActionResponse, error) {
	proc, err := gproc.NewProcess(pid)
	if err != nil {
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}

	switch action {
	case ProcessActionStop:
		if err := proc.TerminateWithContext(ctx); err != nil {
			return nil, fmt.Errorf("terminate process: %w", err)
		}
		if err := waitForExit(ctx, pid, 5*time.Second); err != nil {
			return nil, err
		}
		return &ProcessActionResponse{PID: int(pid), Action: action, Status: "ok", Message: "Process terminated"}, nil
	case ProcessActionForceStop:
		if err := proc.KillWithContext(ctx); err != nil {
			return nil, fmt.Errorf("kill process: %w", err)
		}
		if err := waitForExit(ctx, pid, 5*time.Second); err != nil {
			return nil, err
		}
		return &ProcessActionResponse{PID: int(pid), Action: action, Status: "ok", Message: "Process killed"}, nil
	case ProcessActionSuspend:
		if err := proc.SuspendWithContext(ctx); err != nil {
			return nil, fmt.Errorf("suspend process: %w", err)
		}
		return &ProcessActionResponse{PID: int(pid), Action: action, Status: "ok", Message: "Process suspended"}, nil
	case ProcessActionResume:
		if err := proc.ResumeWithContext(ctx); err != nil {
			return nil, fmt.Errorf("resume process: %w", err)
		}
		return &ProcessActionResponse{PID: int(pid), Action: action, Status: "ok", Message: "Process resumed"}, nil
	case ProcessActionRestart:
		return m.restartProcess(ctx, proc)
	default:
		return nil, fmt.Errorf("unsupported process action: %s", action)
	}
}

func (m *Manager) restartProcess(ctx context.Context, proc *gproc.Process) (*ProcessActionResponse, error) {
	pid := proc.Pid

	exe, err := proc.ExeWithContext(ctx)
	if err != nil || strings.TrimSpace(exe) == "" {
		return nil, errors.New("process executable unavailable")
	}

	args, _ := proc.CmdlineSliceWithContext(ctx)
	if len(args) > 0 {
		args = args[1:]
	}

	cwd, _ := proc.CwdWithContext(ctx)
	env, _ := proc.EnvironWithContext(ctx)

	if err := proc.TerminateWithContext(ctx); err != nil {
		if killErr := proc.KillWithContext(ctx); killErr != nil {
			return nil, fmt.Errorf("terminate process: %w", err)
		}
	}

	if err := waitForExit(ctx, pid, 7*time.Second); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, exe, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = env
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("restart process: %w", err)
	}

	newPID := cmd.Process.Pid
	_ = cmd.Process.Release()

	return &ProcessActionResponse{
		PID:     newPID,
		Action:  ProcessActionRestart,
		Status:  "ok",
		Message: fmt.Sprintf("Restarted as PID %d", newPID),
	}, nil
}

func (m *Manager) buildSummary(ctx context.Context, proc *gproc.Process) (ProcessSummary, error) {
	name, err := proc.NameWithContext(ctx)
	if err != nil {
		name = fmt.Sprintf("pid-%d", proc.Pid)
	}

	cmdline, err := proc.CmdlineSliceWithContext(ctx)
	var command string
	if err == nil && len(cmdline) > 0 {
		command = strings.Join(cmdline, " ")
	}
	if command == "" {
		if exe, err := proc.ExeWithContext(ctx); err == nil && exe != "" {
			command = exe
		}
	}
	if command == "" {
		command = name
	}

	cpu, err := proc.CPUPercentWithContext(ctx)
	if err != nil {
		cpu = 0
	}

	memInfo, err := proc.MemoryInfoWithContext(ctx)
	var memory uint64
	if err == nil && memInfo != nil {
		memory = memInfo.RSS
	}

	statuses, err := proc.StatusWithContext(ctx)
	status := ProcessStatusUnknown
	if err == nil && len(statuses) > 0 {
		status = normalizeStatus(statuses[0])
	}

	ppid, err := proc.PpidWithContext(ctx)
	var parent int32
	if err == nil {
		parent = int32(ppid)
	}

	username, err := proc.UsernameWithContext(ctx)
	if err != nil {
		username = ""
	}

	started := ""
	if created, err := proc.CreateTimeWithContext(ctx); err == nil && created > 0 {
		started = time.UnixMilli(created).UTC().Format(time.RFC3339Nano)
	}

	summary := ProcessSummary{
		PID:       proc.Pid,
		Name:      name,
		CPU:       round(cpu, 2),
		Memory:    memory,
		Status:    status,
		Command:   command,
		User:      username,
		StartedAt: started,
	}
	if parent > 0 {
		summary.PPID = parent
	}
	return summary, nil
}

func normalizeStatus(input string) ProcessStatus {
	value := strings.ToLower(strings.TrimSpace(input))
	switch value {
	case "r", "running":
		return ProcessStatusRunning
	case "s", "sleeping", "sleep":
		return ProcessStatusSleeping
	case "t", "stopped", "stop":
		return ProcessStatusStopped
	case "i", "idle":
		return ProcessStatusIdle
	case "z", "zombie", "defunct":
		return ProcessStatusZombie
	}
	if strings.Contains(value, "suspend") {
		return ProcessStatusSuspended
	}
	if strings.Contains(value, "sleep") {
		return ProcessStatusSleeping
	}
	if strings.Contains(value, "stop") {
		return ProcessStatusStopped
	}
	if strings.Contains(value, "idle") {
		return ProcessStatusIdle
	}
	if strings.Contains(value, "zombie") || strings.Contains(value, "defunct") {
		return ProcessStatusZombie
	}
	if strings.Contains(value, "run") {
		return ProcessStatusRunning
	}
	return ProcessStatusUnknown
}

func round(value float64, precision int) float64 {
	if precision <= 0 {
		return math.Round(value)
	}
	factor := math.Pow10(precision)
	return math.Round(value*factor) / factor
}

func sanitizeArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	cleaned := make([]string, 0, len(args))
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func mergeEnv(base []string, overrides map[string]string) []string {
	env := make([]string, 0, len(base)+len(overrides))
	env = append(env, base...)
	for key, value := range overrides {
		if key == "" {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func pointer[T any](value T) *T {
	return &value
}

func waitForExit(ctx context.Context, pid int32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		exists, err := platform.ProcessExists(int(pid))
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("process did not exit before timeout")
}

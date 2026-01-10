// Code generated from JSON Schema using quicktype. DO NOT EDIT.
// To parse and unparse this JSON data, add this code to your project and do:
//
//    tenvyProtocol, err := UnmarshalTenvyProtocol(bytes)
//    bytes, err = tenvyProtocol.Marshal()

package protocol

import "time"

import "encoding/json"

func UnmarshalTenvyProtocol(data []byte) (TenvyProtocol, error) {
	var r TenvyProtocol
	err := json.Unmarshal(data, &r)
	return r, err
}

func (r *TenvyProtocol) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

// Unified communication protocol between tenvy-server and tenvy-client.
type TenvyProtocol struct {
	AgentControlCommandPayload *AgentControlCommandPayload `json:"agentControlCommandPayload,omitempty"`
	AgentMetadata              *AgentMetadata              `json:"agentMetadata,omitempty"`
	AgentRegistrationRequest   *AgentRegistrationRequest   `json:"agentRegistrationRequest,omitempty"`
	AgentSyncRequest           *AgentSyncRequest           `json:"agentSyncRequest,omitempty"`
	Command                    *Command                    `json:"command,omitempty"`
	CommandResult              *CommandResult              `json:"commandResult,omitempty"`
	PingCommandPayload         *PingCommandPayload         `json:"pingCommandPayload,omitempty"`
	ShellCommandPayload        *ShellCommandPayload        `json:"shellCommandPayload,omitempty"`
}

type AgentControlCommandPayload struct {
	Action Action  `json:"action"`
	Force  *bool   `json:"force,omitempty"`
	Reason *string `json:"reason,omitempty"`
}

type AgentMetadata struct {
	Architecture    string   `json:"architecture"`
	HardwareID      *string  `json:"hardwareId,omitempty"`
	Hostname        string   `json:"hostname"`
	IPAddress       *string  `json:"ipAddress,omitempty"`
	OS              string   `json:"os"`
	PublicIPAddress *string  `json:"publicIpAddress,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Username        string   `json:"username"`
	Version         *string  `json:"version,omitempty"`
}

type AgentRegistrationRequest struct {
	Metadata AgentMetadata `json:"metadata"`
	Token    *string       `json:"token,omitempty"`
}

type AgentSyncRequest struct {
	Metrics   *Metrics        `json:"metrics,omitempty"`
	Results   []CommandResult `json:"results,omitempty"`
	Status    Status          `json:"status"`
	Timestamp time.Time       `json:"timestamp"`
}

type Metrics struct {
	Goroutines    *int64 `json:"goroutines,omitempty"`
	MemoryBytes   *int64 `json:"memoryBytes,omitempty"`
	UptimeSeconds *int64 `json:"uptimeSeconds,omitempty"`
}

type CommandResult struct {
	CommandID   string    `json:"commandId"`
	CompletedAt time.Time `json:"completedAt"`
	Error       *string   `json:"error,omitempty"`
	Output      *string   `json:"output,omitempty"`
	Success     bool      `json:"success"`
}

type Command struct {
	CreatedAt time.Time              `json:"createdAt"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Payload   map[string]interface{} `json:"payload"`
	Signature *string                `json:"signature,omitempty"`
}

type PingCommandPayload struct {
	Message *string `json:"message,omitempty"`
}

type ShellCommandPayload struct {
	Command          string            `json:"command"`
	Elevated         *bool             `json:"elevated,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	TimeoutSeconds   *int64            `json:"timeoutSeconds,omitempty"`
	WorkingDirectory *string           `json:"workingDirectory,omitempty"`
}

type Action string

const (
	Disconnect Action = "disconnect"
	Logoff     Action = "logoff"
	Reconnect  Action = "reconnect"
	Restart    Action = "restart"
	Shutdown   Action = "shutdown"
	Sleep      Action = "sleep"
)

type Status string

const (
	Busy    Status = "busy"
	Offline Status = "offline"
	Online  Status = "online"
)

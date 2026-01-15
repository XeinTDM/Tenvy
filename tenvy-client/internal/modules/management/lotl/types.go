package lotl

import "github.com/rootbay/tenvy-client/internal/protocol"

type LotlCommandPayload struct {
	Action   string            `json:"action"`
	Target   string            `json:"target,omitempty"`
	Source   string            `json:"source,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type LotlResult struct {
	protocol.CommandResult
	Operation string `json:"operation"`
	Output    string `json:"output,omitempty"`
}

package domain

// DispatchMode selects what a dispatched agent does with its Ticket.
type DispatchMode string

const (
	DispatchModeAgent DispatchMode = "agent"
	DispatchModePlan  DispatchMode = "plan"
)

// DispatchError records why a dispatch Session failed.
type DispatchError struct {
	Kind      string `json:"kind,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
	HelpURL   string `json:"helpUrl,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

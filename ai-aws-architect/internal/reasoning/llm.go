// Package reasoning turns a user turn into an assistant reply and, when the
// architecture actually changed, a new config version.
package reasoning

import (
	"context"
	"encoding/json"
)

// Turn is one message in the model-facing conversation.
type Turn struct {
	Role    string // "user" or "assistant"
	Content string
}

// ToolSpec forces structured output: the model must answer by calling this
// tool, so the reply and the config arrive as validated JSON rather than prose
// that has to be scraped.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

type Request struct {
	System   string
	Messages []Turn
	Tool     ToolSpec
	// CurrentConfig is the running config document, or nil for a new chat.
	//
	// Redundant for Bedrock, which already receives it inside System. It exists
	// for the stub, which has no language model to read a prompt with: without
	// it the stub rebuilds from scratch every turn and "add X too" silently
	// replaces the existing blocks instead of extending them.
	CurrentConfig json.RawMessage
}

type Response struct {
	// ToolInput is the JSON the model passed to the forced tool.
	ToolInput json.RawMessage
	// Text is any prose the model emitted alongside the tool call. Usually
	// empty; kept as a fallback when a model declines to use the tool.
	Text         string
	Model        string
	InputTokens  int
	OutputTokens int
	StopReason   string
}

// LLM is the seam between the reasoning and Bedrock. The stub implementation lets
// the whole chat/versioning/diff/revert flow be developed and demoed with no
// AWS calls and no credit burn.
type LLM interface {
	Invoke(ctx context.Context, req Request) (*Response, error)
	ModelID() string

	// Name identifies the implementation, the way runner.Runner.Name does.
	//
	// ModelID is not a substitute: it returns an opaque provider string, so a
	// client cannot tell a real model from a stand-in without matching on
	// magic values. This is the more dangerous of the two stubs to get wrong -
	// a stub plan is visibly simulated, but a stub proposal looks exactly like
	// a real one, and nothing on screen contradicts a UI that claims
	// otherwise.
	Name() string
}

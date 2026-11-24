package infrastructure

import (
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"

	"github.com/HappyLadySauceM/Nagi/internal/intent/domain"
)

const maxSemanticResponseBytes = 1 << 20

func decodeSemanticResponse(r io.Reader, out any, strict bool) error {
	if !strict {
		return json.NewDecoder(io.LimitReader(r, maxSemanticResponseBytes)).Decode(out)
	}
	body, err := io.ReadAll(io.LimitReader(r, maxSemanticResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxSemanticResponseBytes {
		return errors.New("semantic response exceeds limit")
	}
	if len(body) == 0 || !json.Valid(body) {
		return errors.New("invalid semantic response JSON")
	}
	return json.Unmarshal(body, out)
}

type semanticWireUsage struct {
	PromptTokens     json.RawMessage `json:"prompt_tokens"`
	CompletionTokens json.RawMessage `json:"completion_tokens"`
	InputTokens      json.RawMessage `json:"input_tokens"`
	OutputTokens     json.RawMessage `json:"output_tokens"`
}

func (u *semanticWireUsage) value() domain.SemanticUsage {
	if u == nil {
		return domain.SemanticUsage{}
	}
	input := readSemanticTokens(u.PromptTokens, u.InputTokens)
	output := readSemanticTokens(u.CompletionTokens, u.OutputTokens)
	var usage domain.SemanticUsage
	if input != nil && *input >= 0 {
		usage.InputTokens, usage.InputResponses = *input, 1
	}
	if output != nil && *output >= 0 {
		usage.OutputTokens, usage.OutputResponses = *output, 1
	}
	if usage.InputResponses == 1 && usage.OutputResponses == 1 {
		usage.Responses = 1
	}
	return usage
}

func readSemanticTokens(primary, alternate json.RawMessage) *int64 {
	if primary == nil {
		primary = alternate
	}
	var value *int64
	if json.Unmarshal(primary, &value) != nil || value == nil || *value < 0 {
		return nil
	}
	return value
}

func parseSemanticUsage(raw json.RawMessage) domain.SemanticUsage {
	var usage semanticWireUsage
	if json.Unmarshal(raw, &usage) != nil {
		return domain.SemanticUsage{}
	}
	return usage.value()
}

type semanticUsageCounter struct {
	input, output, responses, inputResponses, outputResponses atomic.Int64
}

func (c *semanticUsageCounter) add(u domain.SemanticUsage) {
	c.input.Add(u.InputTokens)
	c.output.Add(u.OutputTokens)
	c.responses.Add(u.Responses)
	c.inputResponses.Add(u.InputResponses)
	c.outputResponses.Add(u.OutputResponses)
}

func (c *semanticUsageCounter) value() domain.SemanticUsage {
	return domain.SemanticUsage{InputTokens: c.input.Load(), OutputTokens: c.output.Load(), Responses: c.responses.Load(), InputResponses: c.inputResponses.Load(), OutputResponses: c.outputResponses.Load()}
}

package eino

import (
	"context"
	"slices"

	audit "github.com/HappyLadySauceM/Nagi/internal/audit/contracts"
	media "github.com/HappyLadySauceM/Nagi/internal/media/contracts"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const inboundPictureTag = "nagi_current_inbound_picture"

type mediaChatModel struct{ model.ToolCallingChatModel }

func (m mediaChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return mediaChatModel{bound}, nil
}

func (m mediaChatModel) GetType() string {
	if typed, ok := m.ToolCallingChatModel.(interface{ GetType() string }); ok {
		return typed.GetType()
	}
	return "OpenAI"
}

func (m mediaChatModel) IsCallbacksEnabled() bool {
	enabled, ok := m.ToolCallingChatModel.(interface{ IsCallbacksEnabled() bool })
	return ok && enabled.IsCallbacksEnabled()
}

func (m mediaChatModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	ctx, messages = observeModelMedia(ctx, messages)
	return m.ToolCallingChatModel.Generate(ctx, messages, opts...)
}

func (m mediaChatModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx, messages = observeModelMedia(ctx, messages)
	return m.ToolCallingChatModel.Stream(ctx, messages, opts...)
}

func observeModelMedia(ctx context.Context, input []*schema.Message) (context.Context, []*schema.Message) {
	shape := media.ModelMediaAttempt{Shape: media.MediaTextOnly}
	messages := slices.Clone(input)
	for i, message := range input {
		if message == nil || message.Role != schema.User {
			continue
		}
		var copy *schema.Message
		for j, part := range message.UserInputMultiContent {
			index, tagged := part.Extra[inboundPictureTag].(int)
			if !tagged {
				continue
			}
			if copy == nil {
				cloned := *message
				cloned.UserInputMultiContent = slices.Clone(message.UserInputMultiContent)
				messages[i] = &cloned
				copy = &cloned
			}
			copy.UserInputMultiContent[j].Extra = nil
			switch {
			case part.Type == schema.ChatMessagePartTypeImageURL && part.Image != nil:
				shape.Images = append(shape.Images, index)
			case part.Type == schema.ChatMessagePartTypeText && part.Text != "":
				shape.Hints = append(shape.Hints, index)
			}
		}
	}
	switch {
	case len(shape.Images) != 0:
		shape.Shape = media.MediaImage
	case len(shape.Hints) != 0:
		shape.Shape = media.MediaHint
	}
	return audit.WithModelMedia(ctx, "generation.media_request", shape), messages
}

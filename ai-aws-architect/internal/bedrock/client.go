// Package bedrock implements reasoning.LLM against Amazon Bedrock's Converse API.
//
// Converse is used rather than InvokeModel because it normalizes the request
// and response shape across models and gives first-class tool support, which
// is how structured output is forced here.
package bedrock

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/cloud-ai/ai-aws-architect/internal/reasoning"
)

type Options struct {
	Region string
	// Profile is optional. Empty means the default credential chain: env vars,
	// SSO cache, ~/.aws/credentials, then the instance or task role once this
	// is deployed. Nothing here ever holds a static key.
	Profile string
	// ModelID must be a model or inference profile enabled in this account and
	// region. Verify with:
	//   aws bedrock list-inference-profiles --region <region>
	// Cross-region inference profiles carry a geo prefix (us. / eu. / apac.).
	ModelID     string
	MaxTokens   int32
	Temperature float32
}

type Client struct {
	api  *bedrockruntime.Client
	opts Options
}

func New(ctx context.Context, opts Options) (*Client, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(opts.Region)}
	if opts.Profile != "" {
		loadOpts = append(loadOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 4096
	}
	return &Client{api: bedrockruntime.NewFromConfig(cfg), opts: opts}, nil
}

func (c *Client) ModelID() string { return c.opts.ModelID }

// Name satisfies reasoning.LLM, mirroring runner.Runner.Name. ModelID is not a
// substitute: it returns an opaque provider string, so a client could only tell
// a real model from the stub by matching on a magic value.
func (c *Client) Name() string { return "bedrock" }

func (c *Client) Invoke(ctx context.Context, req reasoning.Request) (*reasoning.Response, error) {
	messages := make([]types.Message, 0, len(req.Messages))
	for _, t := range req.Messages {
		role := types.ConversationRoleUser
		if t.Role == "assistant" {
			role = types.ConversationRoleAssistant
		}
		messages = append(messages, types.Message{
			Role:    role,
			Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: t.Content}},
		})
	}

	in := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(c.opts.ModelID),
		Messages: messages,
		System: []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: req.System},
		},
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(c.opts.MaxTokens),
			Temperature: aws.Float32(c.opts.Temperature),
		},
	}

	// toolChoice pins the model to this one tool, so every response comes back
	// as schema-shaped JSON instead of prose that has to be parsed.
	if req.Tool.Name != "" {
		in.ToolConfig = &types.ToolConfiguration{
			Tools: []types.Tool{
				&types.ToolMemberToolSpec{
					Value: types.ToolSpecification{
						Name:        aws.String(req.Tool.Name),
						Description: aws.String(req.Tool.Description),
						InputSchema: &types.ToolInputSchemaMemberJson{
							Value: document.NewLazyDocument(req.Tool.InputSchema),
						},
					},
				},
			},
			ToolChoice: &types.ToolChoiceMemberTool{
				Value: types.SpecificToolChoice{Name: aws.String(req.Tool.Name)},
			},
		}
	}

	out, err := c.api.Converse(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("bedrock converse: %w", err)
	}

	resp := &reasoning.Response{Model: c.opts.ModelID, StopReason: string(out.StopReason)}
	if out.Usage != nil {
		resp.InputTokens = int(aws.ToInt32(out.Usage.InputTokens))
		resp.OutputTokens = int(aws.ToInt32(out.Usage.OutputTokens))
	}

	msg, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return nil, fmt.Errorf("unexpected converse output type %T", out.Output)
	}

	for _, block := range msg.Value.Content {
		switch b := block.(type) {
		case *types.ContentBlockMemberText:
			resp.Text += b.Value
		case *types.ContentBlockMemberToolUse:
			if aws.ToString(b.Value.Name) != req.Tool.Name {
				continue
			}
			// The tool input arrives as a smithy document; round-trip it into
			// raw JSON for the reasoning layer to unmarshal.
			var payload any
			if err := b.Value.Input.UnmarshalSmithyDocument(&payload); err != nil {
				return nil, fmt.Errorf("decode tool input: %w", err)
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("encode tool input: %w", err)
			}
			resp.ToolInput = raw
		}
	}
	return resp, nil
}

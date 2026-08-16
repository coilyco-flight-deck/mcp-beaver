package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/calico32/kdl-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ssmGetter interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

type ssmPolicy struct {
	Region    string
	Parameter string
}

var ssmOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"name":{"type":"string"},
		"type":{"type":"string"},
		"value":{"type":"string"},
		"version":{"type":"integer"}
	},
	"required":["name","type","value","version"],
	"additionalProperties":false
}`)

// NewSSM builds an SSM reader whose KDL policy fixes the sole parameter that
// either outward tool may retrieve.
func NewSSM(ctx context.Context, name, specPath string, src []byte) (*Server, error) {
	policy, err := parseSSMPolicy(src)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(policy.Region))
	if err != nil {
		return nil, fmt.Errorf("mcp-beaver: load AWS config: %w", err)
	}
	return newSSMServer(name, specPath, policy, ssm.NewFromConfig(cfg))
}

func newSSMServer(name, specPath string, policy ssmPolicy, client ssmGetter) (*Server, error) {
	tools := []*mcp.Tool{
		{
			Name:         "get_parameter",
			Title:        "Get parameter",
			Description:  "Use this when the user wants to get the one SSM parameter allowed by this ward policy.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
			OutputSchema: ssmOutputSchema,
			Annotations:  readOnlyOpenWorldAnnotations(),
		},
		{
			Name:         "get_forgejo_read_token",
			Title:        "Get Forgejo read token",
			Description:  "Use this when the user wants to get the ward policy's fixed Forgejo read token parameter.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema: ssmOutputSchema,
			Annotations:  readOnlyOpenWorldAnnotations(),
		},
	}
	instrumentation, err := newInstrumentation("ssm", tools)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}
	s := &Server{
		name:           name,
		specPath:       specPath,
		tools:          tools,
		handlers:       make(map[string]mcp.ToolHandler, len(tools)),
		upstreams:      []adminUpstreamResponse{{Kind: "aws-ssm", Mode: "sdk"}},
		sdk:            newSDKServer(name, nil),
		telemetry:      instrumentation,
		requestTimeout: DefaultRequestTimeout,
	}
	parameterHandler := ssmToolHandler(client, policy, true)
	tokenHandler := ssmToolHandler(client, policy, false)
	s.registerTool(tools[0], parameterHandler)
	s.registerTool(tools[1], tokenHandler)
	s.installMiddleware()
	return s, nil
}

func readOnlyOpenWorldAnnotations() *mcp.ToolAnnotations {
	destructive := false
	openWorld := true
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: &destructive,
		IdempotentHint:  true,
		OpenWorldHint:   &openWorld,
	}
}

func ssmToolHandler(client ssmGetter, policy ssmPolicy, callerNamesParameter bool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := policy.Parameter
		if callerNamesParameter {
			var args struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError(fmt.Errorf("invalid tool arguments: %w", err)), nil
			}
			if args.Name != policy.Parameter {
				return toolError(fmt.Errorf("mcp-beaver: parameter %q is outside the ward policy", args.Name)), nil
			}
			name = args.Name
		}
		out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           aws.String(name),
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			return toolError(fmt.Errorf("get SSM parameter: %w", err)), nil
		}
		return ssmToolSuccess(out.Parameter), nil
	}
}

func ssmToolSuccess(parameter *types.Parameter) *mcp.CallToolResult {
	if parameter == nil {
		return toolError(fmt.Errorf("get SSM parameter: AWS returned no parameter"))
	}
	payload := struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Value   string `json:"value"`
		Version int64  `json:"version"`
	}{
		Name:    aws.ToString(parameter.Name),
		Type:    string(parameter.Type),
		Value:   aws.ToString(parameter.Value),
		Version: parameter.Version,
	}
	raw, _ := json.Marshal(payload)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: payload,
	}
}

func parseSSMPolicy(src []byte) (ssmPolicy, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return ssmPolicy{}, fmt.Errorf("mcp-beaver: parse SSM KDL: %w", err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return ssmPolicy{}, fmt.Errorf("mcp-beaver: missing top-level `wrap` node")
	}
	args := wrap.Arguments()
	if len(args) != 4 || args[0].String() != "ward" || args[1].String() != "mcp" || args[2].String() != "aws" || args[3].String() != "ssm" {
		return ssmPolicy{}, fmt.Errorf("mcp-beaver: SSM spec must start `wrap ward mcp aws ssm`")
	}
	policy := ssmPolicy{Region: "us-east-1"}
	seenGrants := map[string]bool{}
	for _, node := range wrap.Children().Nodes {
		switch node.Name() {
		case "region":
			if len(node.Arguments()) != 1 {
				return ssmPolicy{}, fmt.Errorf("mcp-beaver: `region` needs one value")
			}
			policy.Region = node.Arguments()[0].String()
		case "parameter":
			if len(node.Arguments()) != 1 {
				return ssmPolicy{}, fmt.Errorf("mcp-beaver: `parameter` needs one path")
			}
			policy.Parameter = node.Arguments()[0].String()
		case "can":
			grant := node.Arguments()
			if len(grant) != 2 {
				return ssmPolicy{}, fmt.Errorf("mcp-beaver: SSM `can` needs verb and resource")
			}
			key := grant[0].String() + " " + grant[1].String()
			if key != "get parameter" && key != "get forgejo-read-token" {
				return ssmPolicy{}, fmt.Errorf("mcp-beaver: unsupported SSM grant %q", key)
			}
			seenGrants[key] = true
		default:
			return ssmPolicy{}, fmt.Errorf("mcp-beaver: unknown SSM policy node %q", node.Name())
		}
	}
	if policy.Parameter == "" {
		return ssmPolicy{}, fmt.Errorf("mcp-beaver: SSM policy needs an exact `parameter` path")
	}
	if !seenGrants["get parameter"] || !seenGrants["get forgejo-read-token"] || len(seenGrants) != 2 {
		return ssmPolicy{}, fmt.Errorf("mcp-beaver: SSM policy must grant exactly both read tools")
	}
	return policy, nil
}

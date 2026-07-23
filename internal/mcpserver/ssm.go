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

// NewSSM builds an SSM reader whose KDL policy fixes the sole parameter that
// either outward tool may retrieve.
func NewSSM(ctx context.Context, name, specPath string, src []byte) (*Server, error) {
	policy, err := parseSSMPolicy(src)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(policy.Region))
	if err != nil {
		return nil, fmt.Errorf("ward-mcp: load AWS config: %w", err)
	}
	return newSSMServer(name, specPath, policy, ssm.NewFromConfig(cfg))
}

func newSSMServer(name, specPath string, policy ssmPolicy, client ssmGetter) (*Server, error) {
	tools := []*mcp.Tool{
		{
			Name:        "get_parameter",
			Description: "Get the one SSM parameter allowed by this ward policy.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
		},
		{
			Name:        "get_forgejo_read_token",
			Description: "Get the ward policy's fixed Forgejo read token parameter.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		},
	}
	s := &Server{
		name:      name,
		specPath:  specPath,
		tools:     tools,
		upstreams: []adminUpstreamResponse{{Kind: "aws-ssm", Mode: "sdk"}},
		sdk:       mcp.NewServer(&mcp.Implementation{Name: name, Version: "0.1.0"}, nil),
	}
	s.sdk.AddTool(tools[0], ssmToolHandler(client, policy, true))
	s.sdk.AddTool(tools[1], ssmToolHandler(client, policy, false))
	s.installCallRewrite()
	return s, nil
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
				return toolError(fmt.Errorf("ward-mcp: parameter %q is outside the ward policy", args.Name)), nil
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
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}

func parseSSMPolicy(src []byte) (ssmPolicy, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return ssmPolicy{}, fmt.Errorf("ward-mcp: parse SSM KDL: %w", err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return ssmPolicy{}, fmt.Errorf("ward-mcp: missing top-level `wrap` node")
	}
	args := wrap.Arguments()
	if len(args) != 4 || args[0].String() != "ward" || args[1].String() != "mcp" || args[2].String() != "aws" || args[3].String() != "ssm" {
		return ssmPolicy{}, fmt.Errorf("ward-mcp: SSM spec must start `wrap ward mcp aws ssm`")
	}
	policy := ssmPolicy{Region: "us-east-1"}
	seenGrants := map[string]bool{}
	for _, node := range wrap.Children().Nodes {
		switch node.Name() {
		case "region":
			if len(node.Arguments()) != 1 {
				return ssmPolicy{}, fmt.Errorf("ward-mcp: `region` needs one value")
			}
			policy.Region = node.Arguments()[0].String()
		case "parameter":
			if len(node.Arguments()) != 1 {
				return ssmPolicy{}, fmt.Errorf("ward-mcp: `parameter` needs one path")
			}
			policy.Parameter = node.Arguments()[0].String()
		case "can":
			grant := node.Arguments()
			if len(grant) != 2 {
				return ssmPolicy{}, fmt.Errorf("ward-mcp: SSM `can` needs verb and resource")
			}
			key := grant[0].String() + " " + grant[1].String()
			if key != "get parameter" && key != "get forgejo-read-token" {
				return ssmPolicy{}, fmt.Errorf("ward-mcp: unsupported SSM grant %q", key)
			}
			seenGrants[key] = true
		default:
			return ssmPolicy{}, fmt.Errorf("ward-mcp: unknown SSM policy node %q", node.Name())
		}
	}
	if policy.Parameter == "" {
		return ssmPolicy{}, fmt.Errorf("ward-mcp: SSM policy needs an exact `parameter` path")
	}
	if !seenGrants["get parameter"] || !seenGrants["get forgejo-read-token"] || len(seenGrants) != 2 {
		return ssmPolicy{}, fmt.Errorf("ward-mcp: SSM policy must grant exactly both read tools")
	}
	return policy, nil
}

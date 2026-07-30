package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	input *ssm.GetParameterInput
}

func (f *fakeSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.input = in
	return &ssm.GetParameterOutput{Parameter: &types.Parameter{
		Name: aws.String(aws.ToString(in.Name)), Value: aws.String("redacted-test-value"), Type: types.ParameterTypeSecureString,
	}}, nil
}

func TestParseSSMPolicy(t *testing.T) {
	src := []byte(`wrap ward mcp aws ssm {
    region "us-east-1"
    parameter "/forgejo/coilyco-ops/read-token"
    can get parameter
    can get forgejo-read-token
}`)
	got, err := parseSSMPolicy(src)
	if err != nil {
		t.Fatal(err)
	}
	if got.Parameter != "/forgejo/coilyco-ops/read-token" || got.Region != "us-east-1" {
		t.Fatalf("policy = %#v", got)
	}
}

func TestSSMPolicyFailsClosed(t *testing.T) {
	src := []byte(`wrap ward mcp aws ssm {
    parameter "/forgejo/coilyco-ops/read-token"
    can get parameter
    can put parameter
}`)
	if _, err := parseSSMPolicy(src); err == nil {
		t.Fatal("write grant should fail closed")
	}
}

func TestSSMToolsAdvertiseReadMetadataAndStructuredOutput(t *testing.T) {
	s, err := newSSMServer("ssm", "ssm.mcp.kdl", ssmPolicy{
		Region:    "us-east-1",
		Parameter: "/allowed",
	}, &fakeSSM{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(s.tools))
	}
	for _, tool := range s.tools {
		if tool.Title == "" {
			t.Errorf("%s title is empty", tool.Name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Errorf("%s annotations = %#v, want read-only and idempotent", tool.Name, tool.Annotations)
		}
		if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Errorf("%s destructiveHint = %#v, want false", tool.Name, tool.Annotations.DestructiveHint)
		}
		if tool.Annotations.OpenWorldHint == nil || !*tool.Annotations.OpenWorldHint {
			t.Errorf("%s openWorldHint = %#v, want true", tool.Name, tool.Annotations.OpenWorldHint)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s output schema is nil", tool.Name)
		}
	}

	result := ssmToolSuccess(&types.Parameter{
		Name:    aws.String("/allowed"),
		Type:    types.ParameterTypeSecureString,
		Value:   aws.String("redacted-test-value"),
		Version: 7,
	})
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var structured map[string]any
	if err := json.Unmarshal(raw, &structured); err != nil {
		t.Fatal(err)
	}
	if structured["name"] != "/allowed" || structured["version"] != float64(7) {
		t.Errorf("structuredContent = %v", structured)
	}
}

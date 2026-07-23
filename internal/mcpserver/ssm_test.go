package mcpserver

import (
	"context"
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

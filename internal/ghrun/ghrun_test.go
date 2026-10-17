package ghrun

import (
	"context"
	"strings"
	"testing"
)

func fakeRunner(stdout, stderr string) CommandRunner {
	return func(ctx context.Context, prNumber string) ([]byte, []byte) {
		return []byte(stdout), []byte(stderr)
	}
}

const sampleChecks = `[
  {"name": "Terraform Cloud/example-org/example-app_production", "state": "SUCCESS", "link": "https://app.terraform.io/app/example-org/example-app_production/runs/run-example00000001"},
  {"name": "Terraform Cloud/example-org/example-app_staging", "state": "SUCCESS", "link": "https://app.terraform.io/app/example-org/example-app_staging/runs/run-example00000002"},
  {"name": "lint", "state": "SUCCESS", "link": "https://github.com/example-org/example-infra/actions/runs/12345"}
]`

func TestCollectRunIDs_AllWorkspaces(t *testing.T) {
	got, err := CollectRunIDs(context.Background(), fakeRunner(sampleChecks, ""), "123", "")
	if err != nil {
		t.Fatalf("CollectRunIDs() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("CollectRunIDs() = %v, want 2 runs", got)
	}
}

func TestCollectRunIDs_SortedByWorkspace(t *testing.T) {
	got, err := CollectRunIDs(context.Background(), fakeRunner(sampleChecks, ""), "123", "")
	if err != nil {
		t.Fatalf("CollectRunIDs() error = %v", err)
	}
	// "example-app_production" < "example-app_staging" lexically, so production's run comes first.
	if got[0] != "run-example00000001" || got[1] != "run-example00000002" {
		t.Errorf("CollectRunIDs() = %v, want [run-example00000001 run-example00000002]", got)
	}
}

func TestCollectRunIDs_WorkspaceFilter(t *testing.T) {
	got, err := CollectRunIDs(context.Background(), fakeRunner(sampleChecks, ""), "123", "production")
	if err != nil {
		t.Fatalf("CollectRunIDs() error = %v", err)
	}
	if len(got) != 1 || got[0] != "run-example00000001" {
		t.Errorf("CollectRunIDs() = %v, want [run-example00000001]", got)
	}
}

func TestCollectRunIDs_NoMatchingFilter(t *testing.T) {
	_, err := CollectRunIDs(context.Background(), fakeRunner(sampleChecks, ""), "123", "nonexistent")
	if err == nil {
		t.Fatal("CollectRunIDs() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("CollectRunIDs() error = %v, want mention of filter", err)
	}
}

func TestCollectRunIDs_NoTerraformChecks(t *testing.T) {
	_, err := CollectRunIDs(context.Background(), fakeRunner(`[{"name": "lint", "state": "SUCCESS", "link": ""}]`, ""), "123", "")
	if err == nil {
		t.Fatal("CollectRunIDs() error = nil, want error")
	}
}

func TestCollectRunIDs_EmptyStdout(t *testing.T) {
	_, err := CollectRunIDs(context.Background(), fakeRunner("", "gh: authentication required"), "123", "")
	if err == nil {
		t.Fatal("CollectRunIDs() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "authentication required") {
		t.Errorf("CollectRunIDs() error = %v, want gh stderr included", err)
	}
}

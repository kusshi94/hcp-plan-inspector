package tfc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tfe "github.com/hashicorp/go-tfe"
)

// newTestClient builds a Client pointed at a local httptest server instead
// of app.terraform.io.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	tfeClient, err := tfe.NewClient(&tfe.Config{
		Address: server.URL,
		Token:   "test-token",
	})
	if err != nil {
		t.Fatalf("tfe.NewClient() error = %v", err)
	}
	return &Client{tfe: tfeClient}
}

func TestFetchRun_Unauthorized(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := client.FetchRun(context.Background(), "run-abc123")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("FetchRun() error = %v, want mention of 401", err)
	}
}

func TestFetchRun_NotFound(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := client.FetchRun(context.Background(), "run-abc123")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("FetchRun() error = %v, want mention of 404", err)
	}
}

func TestFetchRun_SuccessWithWorkspaceIncluded(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.api+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"data": {
				"id": "run-abc123",
				"type": "runs",
				"attributes": {"status": "planned_and_finished", "has-changes": true, "message": "hello"},
				"relationships": {
					"workspace": {"data": {"id": "ws-1", "type": "workspaces"}},
					"plan": {"data": {"id": "plan-xyz", "type": "plans"}}
				}
			},
			"included": [
				{"id": "ws-1", "type": "workspaces", "attributes": {"name": "example-app_production"}}
			]
		}`))
	})

	run, err := client.FetchRun(context.Background(), "run-abc123")
	if err != nil {
		t.Fatalf("FetchRun() error = %v", err)
	}
	if run.Workspace != "example-app_production" {
		t.Errorf("Workspace = %q, want example-app_production", run.Workspace)
	}
	if run.PlanID != "plan-xyz" {
		t.Errorf("PlanID = %q, want plan-xyz", run.PlanID)
	}
	if run.Status != "planned_and_finished" || !run.HasChanges || run.Message != "hello" {
		t.Errorf("run = %+v, unexpected attributes", run)
	}
}

func TestFetchPlanJSONOutput_ForbiddenIsNonFatalWarning(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	result := client.FetchPlanJSONOutput(context.Background(), "plan-xyz")
	if result.Plan != nil {
		t.Errorf("Plan = %+v, want nil on 403", result.Plan)
	}
	if !strings.Contains(result.Warning, "403") || !strings.Contains(result.Warning, "admin") {
		t.Errorf("Warning = %q, want mention of 403 and admin permission", result.Warning)
	}
}

func TestFetchPlanJSONOutput_Success(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"format_version":"1.2","resource_changes":[{"address":"aws_instance.foo","change":{"actions":["update"]}}]}`))
	})

	result := client.FetchPlanJSONOutput(context.Background(), "plan-xyz")
	if result.Warning != "" {
		t.Fatalf("Warning = %q, want empty", result.Warning)
	}
	if result.Plan == nil || len(result.Plan.ResourceChanges) != 1 {
		t.Fatalf("Plan = %+v, want one resource change", result.Plan)
	}
}

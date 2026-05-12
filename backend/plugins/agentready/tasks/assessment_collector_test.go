package tasks

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchGithubAssessment(t *testing.T) {
	assessmentJSON := `{"overall_score": 85.5, "certification_level": "Gold"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(assessmentJSON))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/repos/myorg/myrepo/contents/.agentready/assessment-latest.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		resp := map[string]interface{}{
			"content":  encoded,
			"encoding": "base64",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	result, err := FetchGithubAssessment(server.URL, "myorg/myrepo", ".agentready/assessment-latest.json", "test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != assessmentJSON {
		t.Errorf("expected %q, got %q", assessmentJSON, result)
	}
}

func TestFetchGithubAssessment_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message": "Not Found"}`))
	}))
	defer server.Close()

	result, err := FetchGithubAssessment(server.URL, "myorg/myrepo", ".agentready/assessment-latest.json", "test-token")
	if err != nil {
		t.Fatalf("404 should not return error, got: %v", err)
	}
	if result != "" {
		t.Errorf("404 should return empty string, got %q", result)
	}
}

func TestFetchGitlabAssessment(t *testing.T) {
	assessmentJSON := `{"overall_score": 72.0, "certification_level": "Silver"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Private-Token") != "glpat-test" {
			t.Errorf("expected Private-Token glpat-test, got %s", r.Header.Get("Private-Token"))
		}
		expectedPath := "/api/v4/projects/42/repository/files/.agentready%2Fassessment-latest.json/raw"
		actualPath := r.URL.RawPath
		if actualPath == "" {
			actualPath = r.URL.Path
		}
		if actualPath != expectedPath {
			t.Errorf("unexpected path: %s, expected: %s", actualPath, expectedPath)
		}
		w.Write([]byte(assessmentJSON))
	}))
	defer server.Close()

	result, err := FetchGitlabAssessment(server.URL, 42, ".agentready/assessment-latest.json", "main", "glpat-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != assessmentJSON {
		t.Errorf("expected %q, got %q", assessmentJSON, result)
	}
}

func TestFetchGitlabAssessment_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	result, err := FetchGitlabAssessment(server.URL, 42, ".agentready/assessment-latest.json", "main", "glpat-test")
	if err != nil {
		t.Fatalf("404 should not return error, got: %v", err)
	}
	if result != "" {
		t.Errorf("404 should return empty string, got %q", result)
	}
}

func TestParseDomainRepoId(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantProvider string
		wantConnId   uint64
		wantScopeId  string
		wantErr      bool
	}{
		{"github", "github:GithubRepo:1:12345", "github", 1, "12345", false},
		{"gitlab", "gitlab:GitlabProject:3:42", "gitlab", 3, "42", false},
		{"invalid", "bad-format", "", 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, connId, scopeId, err := ParseDomainRepoId(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDomainRepoId() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if provider != tt.wantProvider {
				t.Errorf("provider = %v, want %v", provider, tt.wantProvider)
			}
			if connId != tt.wantConnId {
				t.Errorf("connId = %v, want %v", connId, tt.wantConnId)
			}
			if scopeId != tt.wantScopeId {
				t.Errorf("scopeId = %v, want %v", scopeId, tt.wantScopeId)
			}
		})
	}
}

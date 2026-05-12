# AgentReady DevLake Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a DevLake metric plugin that collects `.agentready/assessment-latest.json` from repos connected via GitHub/GitLab plugins and exposes the data through Grafana dashboards.

**Architecture:** Metric plugin running after github/gitlab plugins. Reuses existing connections to fetch assessment files via API. Stores data in tool-layer tables (`_tool_agentready_*`). Grafana dashboards query these tables directly.

**Tech Stack:** Go 1.21+, GORM, DevLake plugin framework, MySQL 8, Grafana JSON dashboards

**Spec:** `docs/superpowers/specs/2026-05-11-agentready-plugin-design.md`

---

## File Map

```
backend/plugins/agentready/
├── agentready.go                         # Plugin entry point
├── impl/
│   └── impl.go                           # Interface implementations
├── models/
│   ├── assessment.go                     # Assessment model
│   ├── finding.go                        # Finding model
│   ├── metric.go                         # Aggregated metrics model
│   ├── scope_config.go                   # Scope configuration model
│   └── migrationscripts/
│       ├── init_schema.go                # Initial schema migration
│       └── register.go                   # Migration registry
├── tasks/
│   ├── task_data.go                      # Options, shared state, helpers
│   ├── assessment_collector.go           # Fetch assessment JSON from APIs
│   ├── assessment_collector_test.go      # Collector unit tests
│   ├── assessment_extractor.go           # Parse raw JSON → assessment rows
│   ├── assessment_extractor_test.go      # Extractor unit tests
│   ├── finding_extractor.go              # Parse findings from assessments
│   ├── finding_extractor_test.go         # Finding extractor tests
│   ├── metrics_calculator.go             # Compute aggregated stats
│   └── metrics_calculator_test.go        # Metrics calculator tests
├── api/
│   ├── init.go                           # API initialization
│   ├── assessments.go                    # Assessment REST endpoints
│   ├── stats.go                          # Stats endpoint
│   └── scope_config.go                   # Scope config CRUD
└── grafana/
    ├── fleet-overview.json               # Fleet overview dashboard
    ├── repo-detail.json                  # Repo detail dashboard
    └── findings-analysis.json            # Findings analysis dashboard
```

---

## Task 1: Models — Assessment, Finding, Metric

**Files:**
- Create: `backend/plugins/agentready/models/assessment.go`
- Create: `backend/plugins/agentready/models/finding.go`
- Create: `backend/plugins/agentready/models/metric.go`
- Create: `backend/plugins/agentready/models/scope_config.go`

- [ ] **Step 1: Create assessment model**

```go
// backend/plugins/agentready/models/assessment.go
package models

import (
	"time"

	"github.com/apache/incubator-devlake/core/models/common"
)

type AgentReadyAssessment struct {
	common.NoPKModel

	Id                 string    `gorm:"primaryKey;type:varchar(255)"`
	RepoId             string    `gorm:"index;type:varchar(255)"`
	RepoName           string    `gorm:"type:varchar(255)"`
	ConnectionId       uint64    `gorm:"index"`
	Provider           string    `gorm:"type:varchar(50)"`
	SchemaVersion      string    `gorm:"type:varchar(20)"`
	OverallScore       float64   `gorm:"type:float"`
	CertificationLevel string    `gorm:"type:varchar(50)"`
	AttributesAssessed int       `gorm:"type:int"`
	AttributesTotal    int       `gorm:"type:int"`
	Branch             string    `gorm:"type:varchar(255)"`
	CommitHash         string    `gorm:"type:varchar(40)"`
	DurationSeconds    float64   `gorm:"type:float"`
	AssessedAt         time.Time `gorm:"index"`
	CollectedAt        time.Time
	RawJSON            string `gorm:"type:longtext"`
}

func (AgentReadyAssessment) TableName() string {
	return "_tool_agentready_assessments"
}

const (
	CertPlatinum         = "Platinum"
	CertGold             = "Gold"
	CertSilver           = "Silver"
	CertBronze           = "Bronze"
	CertNeedsImprovement = "NeedsImprovement"
	CertNone             = "None"
)
```

- [ ] **Step 2: Create finding model**

```go
// backend/plugins/agentready/models/finding.go
package models

import (
	"github.com/apache/incubator-devlake/core/models/common"
)

type AgentReadyFinding struct {
	common.NoPKModel

	Id                 string  `gorm:"primaryKey;type:varchar(255)"`
	AssessmentId       string  `gorm:"index;type:varchar(255)"`
	RepoId             string  `gorm:"index;type:varchar(255)"`
	AttributeId        string  `gorm:"type:varchar(255)"`
	AttributeName      string  `gorm:"type:varchar(255)"`
	Category           string  `gorm:"type:varchar(255)"`
	Tier               int     `gorm:"type:int"`
	Status             string  `gorm:"type:varchar(50)"`
	Score              *float64 `gorm:"type:float"`
	MeasuredValue      string  `gorm:"type:text"`
	Threshold          string  `gorm:"type:text"`
	Evidence           string  `gorm:"type:text"`
	RemediationSummary string  `gorm:"type:text"`
	RemediationSteps   string  `gorm:"type:text"`
	DefaultWeight      float64 `gorm:"type:float"`
}

func (AgentReadyFinding) TableName() string {
	return "_tool_agentready_findings"
}

const (
	FindingStatusPass          = "pass"
	FindingStatusFail          = "fail"
	FindingStatusSkipped       = "skipped"
	FindingStatusError         = "error"
	FindingStatusNotApplicable = "not_applicable"

	TierEssential = 1
	TierCritical  = 2
	TierImportant = 3
	TierAdvanced  = 4
)
```

- [ ] **Step 3: Create metric model**

```go
// backend/plugins/agentready/models/metric.go
package models

import (
	"time"

	"github.com/apache/incubator-devlake/core/models/common"
)

type AgentReadyMetric struct {
	common.NoPKModel

	Id             string    `gorm:"primaryKey;type:varchar(255)"`
	RepoId         string    `gorm:"index;type:varchar(255)"`
	AssessedAt     time.Time `gorm:"index"`
	PassCount      int       `gorm:"type:int"`
	FailCount      int       `gorm:"type:int"`
	SkipCount      int       `gorm:"type:int"`
	Tier1PassRate  float64   `gorm:"type:float"`
	Tier2PassRate  float64   `gorm:"type:float"`
	Tier3PassRate  float64   `gorm:"type:float"`
	Tier4PassRate  float64   `gorm:"type:float"`
	CategoryScores string   `gorm:"type:text"`
}

func (AgentReadyMetric) TableName() string {
	return "_tool_agentready_metrics"
}
```

- [ ] **Step 4: Create scope config model**

```go
// backend/plugins/agentready/models/scope_config.go
package models

import (
	"github.com/apache/incubator-devlake/core/models/common"
)

type AgentReadyScopeConfig struct {
	common.ScopeConfig `mapstructure:",squash" json:",inline" gorm:"embedded"`

	Branch             string `mapstructure:"branch" json:"branch" gorm:"type:varchar(255)"`
	AssessmentFilePath string `mapstructure:"assessmentFilePath" json:"assessmentFilePath" gorm:"type:varchar(500)"`
	ExcludeRepos       string `mapstructure:"excludeRepos" json:"excludeRepos" gorm:"type:text"`
}

func (AgentReadyScopeConfig) TableName() string {
	return "_tool_agentready_scope_configs"
}

const DefaultAssessmentFilePath = ".agentready/assessment-latest.json"

func GetDefaultScopeConfig() *AgentReadyScopeConfig {
	return &AgentReadyScopeConfig{
		AssessmentFilePath: DefaultAssessmentFilePath,
	}
}
```

- [ ] **Step 5: Verify models compile**

Run: `cd /home/bramos/projects/devlake/backend && go build ./plugins/agentready/models/...`
Expected: Clean compile (no errors)

- [ ] **Step 6: Commit**

```bash
git add backend/plugins/agentready/models/
git commit -m "feat(agentready): add data models for assessments, findings, metrics, scope config

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 2: Migration Scripts

**Files:**
- Create: `backend/plugins/agentready/models/migrationscripts/init_schema.go`
- Create: `backend/plugins/agentready/models/migrationscripts/register.go`

- [ ] **Step 1: Create init_schema migration**

```go
// backend/plugins/agentready/models/migrationscripts/init_schema.go
package migrationscripts

import (
	"time"

	"github.com/apache/incubator-devlake/core/context"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/models/common"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/helpers/migrationhelper"
)

var _ plugin.MigrationScript = (*initSchema)(nil)

type initSchema struct{}

type agentReadyAssessment20260511 struct {
	common.NoPKModel
	Id                 string    `gorm:"primaryKey;type:varchar(255)"`
	RepoId             string    `gorm:"index;type:varchar(255)"`
	RepoName           string    `gorm:"type:varchar(255)"`
	ConnectionId       uint64    `gorm:"index"`
	Provider           string    `gorm:"type:varchar(50)"`
	SchemaVersion      string    `gorm:"type:varchar(20)"`
	OverallScore       float64   `gorm:"type:float"`
	CertificationLevel string    `gorm:"type:varchar(50)"`
	AttributesAssessed int       `gorm:"type:int"`
	AttributesTotal    int       `gorm:"type:int"`
	Branch             string    `gorm:"type:varchar(255)"`
	CommitHash         string    `gorm:"type:varchar(40)"`
	DurationSeconds    float64   `gorm:"type:float"`
	AssessedAt         time.Time `gorm:"index"`
	CollectedAt        time.Time
	RawJSON            string `gorm:"type:longtext"`
}

func (agentReadyAssessment20260511) TableName() string {
	return "_tool_agentready_assessments"
}

type agentReadyFinding20260511 struct {
	common.NoPKModel
	Id                 string   `gorm:"primaryKey;type:varchar(255)"`
	AssessmentId       string   `gorm:"index;type:varchar(255)"`
	RepoId             string   `gorm:"index;type:varchar(255)"`
	AttributeId        string   `gorm:"type:varchar(255)"`
	AttributeName      string   `gorm:"type:varchar(255)"`
	Category           string   `gorm:"type:varchar(255)"`
	Tier               int      `gorm:"type:int"`
	Status             string   `gorm:"type:varchar(50)"`
	Score              *float64 `gorm:"type:float"`
	MeasuredValue      string   `gorm:"type:text"`
	Threshold          string   `gorm:"type:text"`
	Evidence           string   `gorm:"type:text"`
	RemediationSummary string   `gorm:"type:text"`
	RemediationSteps   string   `gorm:"type:text"`
	DefaultWeight      float64  `gorm:"type:float"`
}

func (agentReadyFinding20260511) TableName() string {
	return "_tool_agentready_findings"
}

type agentReadyMetric20260511 struct {
	common.NoPKModel
	Id             string    `gorm:"primaryKey;type:varchar(255)"`
	RepoId         string    `gorm:"index;type:varchar(255)"`
	AssessedAt     time.Time `gorm:"index"`
	PassCount      int       `gorm:"type:int"`
	FailCount      int       `gorm:"type:int"`
	SkipCount      int       `gorm:"type:int"`
	Tier1PassRate  float64   `gorm:"type:float"`
	Tier2PassRate  float64   `gorm:"type:float"`
	Tier3PassRate  float64   `gorm:"type:float"`
	Tier4PassRate  float64   `gorm:"type:float"`
	CategoryScores string   `gorm:"type:text"`
}

func (agentReadyMetric20260511) TableName() string {
	return "_tool_agentready_metrics"
}

type agentReadyScopeConfig20260511 struct {
	common.ScopeConfig
	Branch             string `gorm:"type:varchar(255)"`
	AssessmentFilePath string `gorm:"type:varchar(500)"`
	ExcludeRepos       string `gorm:"type:text"`
}

func (agentReadyScopeConfig20260511) TableName() string {
	return "_tool_agentready_scope_configs"
}

func (script *initSchema) Up(basicRes context.BasicRes) errors.Error {
	return migrationhelper.AutoMigrateTables(
		basicRes,
		&agentReadyAssessment20260511{},
		&agentReadyFinding20260511{},
		&agentReadyMetric20260511{},
		&agentReadyScopeConfig20260511{},
	)
}

func (script *initSchema) Version() uint64 {
	return 20260511000001
}

func (script *initSchema) Name() string {
	return "agentready init schema"
}
```

- [ ] **Step 2: Create migration registry**

```go
// backend/plugins/agentready/models/migrationscripts/register.go
package migrationscripts

import (
	"github.com/apache/incubator-devlake/core/plugin"
)

func All() []plugin.MigrationScript {
	return []plugin.MigrationScript{
		&initSchema{},
	}
}
```

- [ ] **Step 3: Verify migrations compile**

Run: `cd /home/bramos/projects/devlake/backend && go build ./plugins/agentready/models/...`
Expected: Clean compile

- [ ] **Step 4: Commit**

```bash
git add backend/plugins/agentready/models/migrationscripts/
git commit -m "feat(agentready): add initial schema migration

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 3: Task Data — Options, Shared State, Helpers

**Files:**
- Create: `backend/plugins/agentready/tasks/task_data.go`

- [ ] **Step 1: Create task data with options, repo info struct, and helpers**

```go
// backend/plugins/agentready/tasks/task_data.go
package tasks

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/apache/incubator-devlake/core/errors"
	helper "github.com/apache/incubator-devlake/helpers/pluginhelper/api"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

type AgentReadyOptions struct {
	ProjectName   string                         `json:"projectName"`
	RepoId        string                         `json:"repoId"`
	TimeAfter     string                         `json:"timeAfter"`
	Branch        string                         `json:"branch"`
	ScopeConfigId uint64                         `json:"scopeConfigId"`
	ScopeConfig   *models.AgentReadyScopeConfig  `json:"scopeConfig"`
}

type AgentReadyTaskData struct {
	Options *AgentReadyOptions
}

// RepoInfo holds the data needed to fetch an assessment from a repo.
type RepoInfo struct {
	DomainRepoId string
	Provider     string // "github" or "gitlab"
	ConnectionId uint64
	// GitHub fields
	FullName string // "owner/repo"
	// GitLab fields
	GitlabId          int
	PathWithNamespace string
	DefaultBranch     string
	// Connection credentials
	Endpoint string
	Token    string
}

func DecodeTaskOptions(options map[string]interface{}) (*AgentReadyOptions, errors.Error) {
	var op AgentReadyOptions
	if err := helper.Decode(options, &op, nil); err != nil {
		return nil, errors.BadInput.Wrap(err, "failed to decode agentready options")
	}
	return &op, nil
}

func ValidateTaskOptions(op *AgentReadyOptions) errors.Error {
	if op.RepoId == "" && op.ProjectName == "" {
		return errors.BadInput.New("either repoId or projectName is required")
	}
	return nil
}

// ParseDomainRepoId extracts provider, connectionId, and scopeId from a domain repo ID.
// Format: "{plugin}:{Model}:{connectionId}:{scopeId}" e.g. "github:GithubRepo:1:12345"
func ParseDomainRepoId(repoId string) (provider string, connectionId uint64, scopeId string, err errors.Error) {
	parts := strings.SplitN(repoId, ":", 4)
	if len(parts) < 4 {
		return "", 0, "", errors.BadInput.New(fmt.Sprintf("invalid domain repo ID format: %s", repoId))
	}
	provider = parts[0]
	connId, parseErr := strconv.ParseUint(parts[2], 10, 64)
	if parseErr != nil {
		return "", 0, "", errors.BadInput.Wrap(parseErr, fmt.Sprintf("invalid connectionId in repo ID: %s", repoId))
	}
	return provider, connId, parts[3], nil
}
```

- [ ] **Step 2: Verify compiles**

Run: `cd /home/bramos/projects/devlake/backend && go build ./plugins/agentready/tasks/...`
Expected: Clean compile

- [ ] **Step 3: Commit**

```bash
git add backend/plugins/agentready/tasks/task_data.go
git commit -m "feat(agentready): add task options, shared state, and repo ID parsing helpers

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 4: Assessment Collector — Fetch from GitHub/GitLab APIs

**Files:**
- Create: `backend/plugins/agentready/tasks/assessment_collector.go`
- Create: `backend/plugins/agentready/tasks/assessment_collector_test.go`

- [ ] **Step 1: Write collector test**

```go
// backend/plugins/agentready/tasks/assessment_collector_test.go
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
		if r.URL.Path != expectedPath {
			t.Errorf("unexpected path: %s, expected: %s", r.URL.Path, expectedPath)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/tasks/... -run TestFetch -v`
Expected: FAIL — `FetchGithubAssessment` and `FetchGitlabAssessment` not defined

- [ ] **Step 3: Implement collector functions and subtask**

```go
// backend/plugins/agentready/tasks/assessment_collector.go
package tasks

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

var CollectAssessmentsMeta = plugin.SubTaskMeta{
	Name:             "collectAssessments",
	EntryPoint:       CollectAssessments,
	EnabledByDefault: true,
	Description:      "Fetch assessment JSON files from connected GitHub/GitLab repositories",
	DomainTypes:      []string{plugin.DOMAIN_TYPE_CODE},
}

// Minimal structs for loading connection credentials from tool tables.
// Token uses serializer:encdec so GORM auto-decrypts.
type githubConn struct {
	ID       uint64 `gorm:"primaryKey;column:id"`
	Endpoint string `gorm:"column:endpoint"`
	Token    string `gorm:"column:token;serializer:encdec"`
}

func (githubConn) TableName() string { return "_tool_github_connections" }

type gitlabConn struct {
	ID       uint64 `gorm:"primaryKey;column:id"`
	Endpoint string `gorm:"column:endpoint"`
	Token    string `gorm:"column:token;serializer:encdec"`
}

func (gitlabConn) TableName() string { return "_tool_gitlab_connections" }

type githubRepoRow struct {
	ConnectionId uint64 `gorm:"column:connection_id"`
	GithubId     int    `gorm:"column:github_id"`
	FullName     string `gorm:"column:full_name"`
}

func (githubRepoRow) TableName() string { return "_tool_github_repos" }

type gitlabProjectRow struct {
	ConnectionId      uint64 `gorm:"column:connection_id"`
	GitlabId          int    `gorm:"column:gitlab_id"`
	PathWithNamespace string `gorm:"column:path_with_namespace"`
	DefaultBranch     string `gorm:"column:default_branch"`
}

func (gitlabProjectRow) TableName() string { return "_tool_gitlab_projects" }

type projectMappingRow struct {
	ProjectName string `gorm:"column:project_name"`
	Table       string `gorm:"column:table"`
	RowId       string `gorm:"column:row_id"`
}

func (projectMappingRow) TableName() string { return "project_mapping" }

func CollectAssessments(taskCtx plugin.SubTaskContext) errors.Error {
	db := taskCtx.GetDal()
	logger := taskCtx.GetLogger()
	data := taskCtx.GetData().(*AgentReadyTaskData)
	config := data.Options.ScopeConfig
	if config == nil {
		config = models.GetDefaultScopeConfig()
	}

	filePath := config.AssessmentFilePath
	if filePath == "" {
		filePath = models.DefaultAssessmentFilePath
	}

	repos, err := discoverRepos(db, data.Options, logger)
	if err != nil {
		return err
	}

	logger.Info("Discovered %d repos for agentready collection", len(repos))
	taskCtx.SetProgress(0, len(repos))

	now := time.Now()
	for _, repo := range repos {
		var rawJSON string
		var fetchErr error

		branch := data.Options.Branch
		if branch == "" && config.Branch != "" {
			branch = config.Branch
		}
		if branch == "" {
			branch = repo.DefaultBranch
		}

		switch repo.Provider {
		case "github":
			endpoint := repo.Endpoint
			if endpoint == "" {
				endpoint = "https://api.github.com"
			}
			rawJSON, fetchErr = FetchGithubAssessment(endpoint, repo.FullName, filePath, repo.Token)
		case "gitlab":
			endpoint := repo.Endpoint
			if endpoint == "" {
				endpoint = "https://gitlab.com"
			}
			rawJSON, fetchErr = FetchGitlabAssessment(endpoint, repo.GitlabId, filePath, branch, repo.Token)
		default:
			logger.Warn(nil, "Unsupported provider %s for repo %s, skipping", repo.Provider, repo.DomainRepoId)
			taskCtx.IncProgress(1)
			continue
		}

		if fetchErr != nil {
			logger.Warn(nil, "Failed to fetch assessment for repo %s: %v", repo.DomainRepoId, fetchErr)
			taskCtx.IncProgress(1)
			continue
		}
		if rawJSON == "" {
			logger.Info("No assessment file found for repo %s, skipping", repo.DomainRepoId)
			taskCtx.IncProgress(1)
			continue
		}

		assessment := &models.AgentReadyAssessment{
			RepoId:       repo.DomainRepoId,
			ConnectionId: repo.ConnectionId,
			Provider:     repo.Provider,
			CollectedAt:  now,
			RawJSON:      rawJSON,
		}

		if repo.FullName != "" {
			assessment.RepoName = repo.FullName
		} else {
			assessment.RepoName = repo.PathWithNamespace
		}

		dbErr := db.CreateOrUpdate(assessment)
		if dbErr != nil {
			logger.Warn(dbErr, "Failed to save raw assessment for repo %s", repo.DomainRepoId)
		}
		taskCtx.IncProgress(1)
	}

	return nil
}

func discoverRepos(db dal.Dal, options *AgentReadyOptions, logger plugin.Logger) ([]*RepoInfo, errors.Error) {
	var repoIds []string

	if options.ProjectName != "" {
		var mappings []projectMappingRow
		err := db.All(&mappings,
			dal.From(&projectMappingRow{}),
			dal.Where("project_name = ? AND `table` = ?", options.ProjectName, "repos"),
		)
		if err != nil {
			return nil, errors.Default.Wrap(err, "failed to query project_mapping")
		}
		for _, m := range mappings {
			repoIds = append(repoIds, m.RowId)
		}
	} else {
		repoIds = []string{options.RepoId}
	}

	var repos []*RepoInfo
	for _, repoId := range repoIds {
		provider, connId, scopeId, err := ParseDomainRepoId(repoId)
		if err != nil {
			logger.Warn(err, "Skipping unparseable repo ID: %s", repoId)
			continue
		}

		info := &RepoInfo{
			DomainRepoId: repoId,
			Provider:     provider,
			ConnectionId: connId,
		}

		switch provider {
		case "github":
			scopeIdInt, parseErr := strconv.Atoi(scopeId)
			if parseErr != nil {
				logger.Warn(nil, "Invalid GitHub scope ID %s in repo %s", scopeId, repoId)
				continue
			}
			var repo githubRepoRow
			dbErr := db.First(&repo, dal.Where("connection_id = ? AND github_id = ?", connId, scopeIdInt))
			if dbErr != nil {
				logger.Warn(dbErr, "GitHub repo not found for connection=%d github_id=%d", connId, scopeIdInt)
				continue
			}
			info.FullName = repo.FullName

			var conn githubConn
			dbErr = db.First(&conn, dal.Where("id = ?", connId))
			if dbErr != nil {
				logger.Warn(dbErr, "GitHub connection %d not found", connId)
				continue
			}
			info.Endpoint = conn.Endpoint
			info.Token = conn.Token

		case "gitlab":
			scopeIdInt, parseErr := strconv.Atoi(scopeId)
			if parseErr != nil {
				logger.Warn(nil, "Invalid GitLab scope ID %s in repo %s", scopeId, repoId)
				continue
			}
			var project gitlabProjectRow
			dbErr := db.First(&project, dal.Where("connection_id = ? AND gitlab_id = ?", connId, scopeIdInt))
			if dbErr != nil {
				logger.Warn(dbErr, "GitLab project not found for connection=%d gitlab_id=%d", connId, scopeIdInt)
				continue
			}
			info.GitlabId = project.GitlabId
			info.PathWithNamespace = project.PathWithNamespace
			info.DefaultBranch = project.DefaultBranch

			var conn gitlabConn
			dbErr = db.First(&conn, dal.Where("id = ?", connId))
			if dbErr != nil {
				logger.Warn(dbErr, "GitLab connection %d not found", connId)
				continue
			}
			info.Endpoint = conn.Endpoint
			info.Token = conn.Token

		default:
			logger.Warn(nil, "Unsupported provider %s for repo %s", provider, repoId)
			continue
		}

		repos = append(repos, info)
	}

	return repos, nil
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// FetchGithubAssessment fetches a file from GitHub Contents API.
// Returns empty string for 404 (no file). Returns error for other failures.
func FetchGithubAssessment(endpoint, fullName, filePath, token string) (string, error) {
	endpoint = strings.TrimSuffix(endpoint, "/")
	apiURL := fmt.Sprintf("%s/repos/%s/contents/%s", endpoint, fullName, filePath)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching from GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding GitHub response: %w", err)
	}

	if result.Encoding != "base64" {
		return "", fmt.Errorf("unexpected encoding: %s", result.Encoding)
	}

	decoded, err := base64.StdEncoding.DecodeString(result.Content)
	if err != nil {
		return "", fmt.Errorf("decoding base64 content: %w", err)
	}

	return string(decoded), nil
}

// FetchGitlabAssessment fetches a file from GitLab Repository Files API.
// Returns empty string for 404. Returns error for other failures.
func FetchGitlabAssessment(endpoint string, projectId int, filePath, branch, token string) (string, error) {
	endpoint = strings.TrimSuffix(endpoint, "/")
	encodedPath := url.PathEscape(filePath)
	apiURL := fmt.Sprintf("%s/api/v4/projects/%d/repository/files/%s/raw", endpoint, projectId, encodedPath)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	if branch != "" {
		q := req.URL.Query()
		q.Set("ref", branch)
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Set("Private-Token", token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching from GitLab: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("GitLab API returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading GitLab response: %w", err)
	}

	return string(body), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/tasks/... -run "TestFetch|TestParseDomain" -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add backend/plugins/agentready/tasks/assessment_collector.go backend/plugins/agentready/tasks/assessment_collector_test.go
git commit -m "feat(agentready): add assessment collector with GitHub/GitLab API fetching

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 5: Assessment Extractor — Parse Raw JSON

**Files:**
- Create: `backend/plugins/agentready/tasks/assessment_extractor.go`
- Create: `backend/plugins/agentready/tasks/assessment_extractor_test.go`

- [ ] **Step 1: Write extractor test**

```go
// backend/plugins/agentready/tasks/assessment_extractor_test.go
package tasks

import (
	"testing"
	"time"

	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

func TestParseAssessmentJSON(t *testing.T) {
	rawJSON := `{
		"schema_version": "1.0.0",
		"repository": {
			"name": "my-repo",
			"branch": "main",
			"commit_hash": "abc123def456abc123def456abc123def456abc1"
		},
		"timestamp": "2026-05-10T14:30:00Z",
		"overall_score": 85.5,
		"certification_level": "Gold",
		"attributes_assessed": 20,
		"attributes_total": 25,
		"duration_seconds": 12.3,
		"findings": [
			{
				"attribute": {
					"id": "doc-readme",
					"name": "README Quality",
					"category": "Documentation Standards",
					"tier": 1,
					"default_weight": 0.8
				},
				"status": "pass",
				"score": 100.0,
				"measured_value": "README exists with 500 words",
				"threshold": "README with >100 words",
				"evidence": ["README.md found", "500 words detected"]
			},
			{
				"attribute": {
					"id": "sec-secrets",
					"name": "No Secrets",
					"category": "Security",
					"tier": 2,
					"default_weight": 0.9
				},
				"status": "fail",
				"score": 30.0,
				"measured_value": "2 potential secrets found",
				"threshold": "0 secrets",
				"evidence": [".env file contains API_KEY"],
				"remediation": {
					"summary": "Remove secrets from codebase",
					"steps": ["Add .env to .gitignore", "Rotate exposed keys"]
				}
			},
			{
				"attribute": {
					"id": "na-attr",
					"name": "N/A Attribute",
					"category": "Other",
					"tier": 4,
					"default_weight": 0.1
				},
				"status": "not_applicable"
			}
		]
	}`

	assessment := &models.AgentReadyAssessment{
		RepoId:       "github:GithubRepo:1:123",
		ConnectionId: 1,
		Provider:     "github",
		RepoName:     "myorg/my-repo",
		CollectedAt:  time.Now(),
		RawJSON:      rawJSON,
	}

	result, err := ParseAssessmentJSON(assessment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.OverallScore != 85.5 {
		t.Errorf("OverallScore = %v, want 85.5", result.OverallScore)
	}
	if result.CertificationLevel != "Gold" {
		t.Errorf("CertificationLevel = %v, want Gold", result.CertificationLevel)
	}
	if result.CommitHash != "abc123def456abc123def456abc123def456abc1" {
		t.Errorf("CommitHash = %v, want abc123...", result.CommitHash)
	}
	if result.Id != "github:GithubRepo:1:123:abc123def456abc123def456abc123def456abc1" {
		t.Errorf("Id = %v, want composite key with repo:commit", result.Id)
	}
	if result.SchemaVersion != "1.0.0" {
		t.Errorf("SchemaVersion = %v, want 1.0.0", result.SchemaVersion)
	}
	if result.AttributesAssessed != 20 {
		t.Errorf("AttributesAssessed = %v, want 20", result.AttributesAssessed)
	}
	if result.Branch != "main" {
		t.Errorf("Branch = %v, want main", result.Branch)
	}
}

func TestParseFindings(t *testing.T) {
	rawJSON := `{
		"schema_version": "1.0.0",
		"repository": {"name": "r", "branch": "main", "commit_hash": "aaa"},
		"timestamp": "2026-05-10T14:30:00Z",
		"overall_score": 50,
		"certification_level": "Bronze",
		"attributes_assessed": 2,
		"attributes_total": 3,
		"duration_seconds": 1,
		"findings": [
			{
				"attribute": {"id": "a1", "name": "A1", "category": "Cat1", "tier": 1, "default_weight": 0.5},
				"status": "pass",
				"score": 100.0,
				"evidence": ["ok"]
			},
			{
				"attribute": {"id": "a2", "name": "A2", "category": "Cat2", "tier": 2, "default_weight": 0.7},
				"status": "fail",
				"score": 30.0,
				"remediation": {"summary": "Fix it", "steps": ["step1", "step2"]}
			},
			{
				"attribute": {"id": "a3", "name": "A3", "category": "Cat3", "tier": 3, "default_weight": 0.1},
				"status": "not_applicable"
			}
		]
	}`

	assessmentId := "repo1:aaa"
	repoId := "repo1"

	findings, err := ParseFindings(rawJSON, assessmentId, repoId)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// not_applicable filtered out
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings (not_applicable filtered), got %d", len(findings))
	}

	f1 := findings[0]
	if f1.AttributeId != "a1" {
		t.Errorf("finding[0].AttributeId = %v, want a1", f1.AttributeId)
	}
	if f1.Status != "pass" {
		t.Errorf("finding[0].Status = %v, want pass", f1.Status)
	}
	if f1.Score == nil || *f1.Score != 100.0 {
		t.Errorf("finding[0].Score = %v, want 100.0", f1.Score)
	}

	f2 := findings[1]
	if f2.RemediationSummary != "Fix it" {
		t.Errorf("finding[1].RemediationSummary = %v, want 'Fix it'", f2.RemediationSummary)
	}
	if f2.Tier != 2 {
		t.Errorf("finding[1].Tier = %v, want 2", f2.Tier)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/tasks/... -run "TestParseAssessment|TestParseFindings" -v`
Expected: FAIL — `ParseAssessmentJSON` and `ParseFindings` not defined

- [ ] **Step 3: Implement extractor**

```go
// backend/plugins/agentready/tasks/assessment_extractor.go
package tasks

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

var ExtractAssessmentsMeta = plugin.SubTaskMeta{
	Name:             "extractAssessments",
	EntryPoint:       ExtractAssessments,
	EnabledByDefault: true,
	Description:      "Parse raw assessment JSON into structured assessment and finding rows",
	DomainTypes:      []string{plugin.DOMAIN_TYPE_CODE},
	Dependencies:     []*plugin.SubTaskMeta{&CollectAssessmentsMeta},
}

type assessmentJSON struct {
	SchemaVersion      string          `json:"schema_version"`
	Repository         repositoryJSON  `json:"repository"`
	Timestamp          string          `json:"timestamp"`
	OverallScore       float64         `json:"overall_score"`
	CertificationLevel string          `json:"certification_level"`
	AttributesAssessed int             `json:"attributes_assessed"`
	AttributesTotal    int             `json:"attributes_total"`
	DurationSeconds    float64         `json:"duration_seconds"`
	Findings           []findingJSON   `json:"findings"`
}

type repositoryJSON struct {
	Name       string `json:"name"`
	Branch     string `json:"branch"`
	CommitHash string `json:"commit_hash"`
}

type findingJSON struct {
	Attribute   attributeJSON    `json:"attribute"`
	Status      string           `json:"status"`
	Score       *float64         `json:"score"`
	MeasuredVal string           `json:"measured_value"`
	Threshold   string           `json:"threshold"`
	Evidence    []string         `json:"evidence"`
	Remediation *remediationJSON `json:"remediation"`
}

type attributeJSON struct {
	Id            string  `json:"id"`
	Name          string  `json:"name"`
	Category      string  `json:"category"`
	Tier          int     `json:"tier"`
	DefaultWeight float64 `json:"default_weight"`
}

type remediationJSON struct {
	Summary string   `json:"summary"`
	Steps   []string `json:"steps"`
}

func ExtractAssessments(taskCtx plugin.SubTaskContext) errors.Error {
	db := taskCtx.GetDal()
	logger := taskCtx.GetLogger()

	// Read all raw assessments that have RawJSON but no Id yet (not yet parsed)
	var rawAssessments []models.AgentReadyAssessment
	err := db.All(&rawAssessments,
		dal.From(&models.AgentReadyAssessment{}),
		dal.Where("raw_json != '' AND id = ''"),
	)
	if err != nil {
		// If none found with empty ID, read all and re-parse
		err = db.All(&rawAssessments,
			dal.From(&models.AgentReadyAssessment{}),
			dal.Where("raw_json != ''"),
		)
		if err != nil {
			return errors.Default.Wrap(err, "failed to query raw assessments")
		}
	}

	logger.Info("Extracting %d assessments", len(rawAssessments))
	taskCtx.SetProgress(0, len(rawAssessments))

	for i := range rawAssessments {
		parsed, parseErr := ParseAssessmentJSON(&rawAssessments[i])
		if parseErr != nil {
			logger.Warn(nil, "Failed to parse assessment for repo %s: %v", rawAssessments[i].RepoId, parseErr)
			taskCtx.IncProgress(1)
			continue
		}

		dbErr := db.CreateOrUpdate(parsed)
		if dbErr != nil {
			logger.Warn(dbErr, "Failed to save parsed assessment %s", parsed.Id)
		}

		findings, findErr := ParseFindings(rawAssessments[i].RawJSON, parsed.Id, parsed.RepoId)
		if findErr != nil {
			logger.Warn(nil, "Failed to parse findings for assessment %s: %v", parsed.Id, findErr)
			taskCtx.IncProgress(1)
			continue
		}

		for _, f := range findings {
			dbErr = db.CreateOrUpdate(f)
			if dbErr != nil {
				logger.Warn(dbErr, "Failed to save finding %s", f.Id)
			}
		}

		taskCtx.IncProgress(1)
	}

	return nil
}

// ParseAssessmentJSON parses raw JSON and populates the assessment fields.
func ParseAssessmentJSON(assessment *models.AgentReadyAssessment) (*models.AgentReadyAssessment, error) {
	var parsed assessmentJSON
	if err := json.Unmarshal([]byte(assessment.RawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("parsing assessment JSON: %w", err)
	}

	assessedAt, err := time.Parse(time.RFC3339, parsed.Timestamp)
	if err != nil {
		assessedAt = assessment.CollectedAt
	}

	assessment.Id = fmt.Sprintf("%s:%s", assessment.RepoId, parsed.Repository.CommitHash)
	assessment.SchemaVersion = parsed.SchemaVersion
	assessment.OverallScore = parsed.OverallScore
	assessment.CertificationLevel = parsed.CertificationLevel
	assessment.AttributesAssessed = parsed.AttributesAssessed
	assessment.AttributesTotal = parsed.AttributesTotal
	assessment.Branch = parsed.Repository.Branch
	assessment.CommitHash = parsed.Repository.CommitHash
	assessment.DurationSeconds = parsed.DurationSeconds
	assessment.AssessedAt = assessedAt

	return assessment, nil
}

// ParseFindings extracts individual findings from raw assessment JSON.
// Filters out not_applicable findings.
func ParseFindings(rawJSON, assessmentId, repoId string) ([]*models.AgentReadyFinding, error) {
	var parsed assessmentJSON
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil {
		return nil, fmt.Errorf("parsing findings JSON: %w", err)
	}

	var findings []*models.AgentReadyFinding
	for _, f := range parsed.Findings {
		if f.Status == models.FindingStatusNotApplicable {
			continue
		}

		finding := &models.AgentReadyFinding{
			Id:            fmt.Sprintf("%s:%s", assessmentId, f.Attribute.Id),
			AssessmentId:  assessmentId,
			RepoId:        repoId,
			AttributeId:   f.Attribute.Id,
			AttributeName: f.Attribute.Name,
			Category:      f.Attribute.Category,
			Tier:          f.Attribute.Tier,
			Status:        f.Status,
			Score:         f.Score,
			MeasuredValue: f.MeasuredVal,
			Threshold:     f.Threshold,
			DefaultWeight: f.Attribute.DefaultWeight,
		}

		if len(f.Evidence) > 0 {
			evidenceJSON, _ := json.Marshal(f.Evidence)
			finding.Evidence = string(evidenceJSON)
		}

		if f.Remediation != nil {
			finding.RemediationSummary = f.Remediation.Summary
			if len(f.Remediation.Steps) > 0 {
				stepsJSON, _ := json.Marshal(f.Remediation.Steps)
				finding.RemediationSteps = string(stepsJSON)
			}
		}

		findings = append(findings, finding)
	}

	return findings, nil
}
```

Note: The `ExtractAssessments` subtask combines assessment extraction and finding extraction into one pass. The spec mentioned a separate `ExtractFindings` subtask, but since both parse the same JSON, combining them avoids reading `raw_json` twice. If you want them separate for independent testability, split `ExtractAssessments` to only save assessment rows, and create a `FindingExtractor` that reads `raw_json` from saved assessments. The tests above already validate both `ParseAssessmentJSON` and `ParseFindings` independently.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/tasks/... -run "TestParseAssessment|TestParseFindings" -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add backend/plugins/agentready/tasks/assessment_extractor.go backend/plugins/agentready/tasks/assessment_extractor_test.go
git commit -m "feat(agentready): add assessment extractor with JSON parsing and finding extraction

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 6: Metrics Calculator

**Files:**
- Create: `backend/plugins/agentready/tasks/metrics_calculator.go`
- Create: `backend/plugins/agentready/tasks/metrics_calculator_test.go`

- [ ] **Step 1: Write metrics calculator test**

```go
// backend/plugins/agentready/tasks/metrics_calculator_test.go
package tasks

import (
	"testing"

	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

func TestCalculateMetricsFromFindings(t *testing.T) {
	score100 := 100.0
	score30 := 30.0

	findings := []*models.AgentReadyFinding{
		{Tier: 1, Status: "pass", Score: &score100, Category: "Docs", DefaultWeight: 0.5},
		{Tier: 1, Status: "fail", Score: &score30, Category: "Docs", DefaultWeight: 0.5},
		{Tier: 2, Status: "pass", Score: &score100, Category: "Security", DefaultWeight: 0.8},
		{Tier: 3, Status: "pass", Score: &score100, Category: "Quality", DefaultWeight: 0.3},
		{Tier: 3, Status: "skipped", Score: nil, Category: "Quality", DefaultWeight: 0.3},
	}

	metric := CalculateMetricsFromFindings(findings)

	if metric.PassCount != 3 {
		t.Errorf("PassCount = %d, want 3", metric.PassCount)
	}
	if metric.FailCount != 1 {
		t.Errorf("FailCount = %d, want 1", metric.FailCount)
	}
	if metric.SkipCount != 1 {
		t.Errorf("SkipCount = %d, want 1", metric.SkipCount)
	}
	// Tier 1: 1 pass, 1 fail = 50%
	if metric.Tier1PassRate != 50.0 {
		t.Errorf("Tier1PassRate = %v, want 50.0", metric.Tier1PassRate)
	}
	// Tier 2: 1 pass, 0 fail = 100%
	if metric.Tier2PassRate != 100.0 {
		t.Errorf("Tier2PassRate = %v, want 100.0", metric.Tier2PassRate)
	}
	// Tier 3: 1 pass, 0 fail (skipped excluded) = 100%
	if metric.Tier3PassRate != 100.0 {
		t.Errorf("Tier3PassRate = %v, want 100.0", metric.Tier3PassRate)
	}
	// Tier 4: no findings = 0%
	if metric.Tier4PassRate != 0.0 {
		t.Errorf("Tier4PassRate = %v, want 0.0", metric.Tier4PassRate)
	}
	// CategoryScores should be valid JSON
	if metric.CategoryScores == "" {
		t.Error("CategoryScores should not be empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/tasks/... -run TestCalculateMetrics -v`
Expected: FAIL — `CalculateMetricsFromFindings` not defined

- [ ] **Step 3: Implement metrics calculator**

```go
// backend/plugins/agentready/tasks/metrics_calculator.go
package tasks

import (
	"encoding/json"
	"fmt"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

var CalculateMetricsMeta = plugin.SubTaskMeta{
	Name:             "calculateMetrics",
	EntryPoint:       CalculateMetrics,
	EnabledByDefault: true,
	Description:      "Compute aggregated pass rates and category scores per assessment",
	DomainTypes:      []string{plugin.DOMAIN_TYPE_CODE},
	Dependencies:     []*plugin.SubTaskMeta{&ExtractAssessmentsMeta},
}

func CalculateMetrics(taskCtx plugin.SubTaskContext) errors.Error {
	db := taskCtx.GetDal()
	logger := taskCtx.GetLogger()

	var assessments []models.AgentReadyAssessment
	err := db.All(&assessments,
		dal.From(&models.AgentReadyAssessment{}),
		dal.Where("id != ''"),
	)
	if err != nil {
		return errors.Default.Wrap(err, "failed to query assessments")
	}

	logger.Info("Calculating metrics for %d assessments", len(assessments))
	taskCtx.SetProgress(0, len(assessments))

	for _, assessment := range assessments {
		var findings []*models.AgentReadyFinding
		err := db.All(&findings,
			dal.From(&models.AgentReadyFinding{}),
			dal.Where("assessment_id = ?", assessment.Id),
		)
		if err != nil {
			logger.Warn(err, "Failed to query findings for assessment %s", assessment.Id)
			taskCtx.IncProgress(1)
			continue
		}

		metric := CalculateMetricsFromFindings(findings)
		metric.Id = fmt.Sprintf("%s:%s", assessment.RepoId, assessment.AssessedAt.Format("20060102T150405"))
		metric.RepoId = assessment.RepoId
		metric.AssessedAt = assessment.AssessedAt

		dbErr := db.CreateOrUpdate(metric)
		if dbErr != nil {
			logger.Warn(dbErr, "Failed to save metric %s", metric.Id)
		}
		taskCtx.IncProgress(1)
	}

	return nil
}

// CalculateMetricsFromFindings computes aggregate stats from a slice of findings.
func CalculateMetricsFromFindings(findings []*models.AgentReadyFinding) *models.AgentReadyMetric {
	metric := &models.AgentReadyMetric{}

	tierPass := map[int]int{1: 0, 2: 0, 3: 0, 4: 0}
	tierTotal := map[int]int{1: 0, 2: 0, 3: 0, 4: 0}
	catScoreSum := map[string]float64{}
	catCount := map[string]int{}

	for _, f := range findings {
		switch f.Status {
		case models.FindingStatusPass:
			metric.PassCount++
			tierPass[f.Tier]++
			tierTotal[f.Tier]++
		case models.FindingStatusFail:
			metric.FailCount++
			tierTotal[f.Tier]++
		default:
			metric.SkipCount++
		}

		if f.Score != nil {
			catScoreSum[f.Category] += *f.Score
			catCount[f.Category]++
		}
	}

	metric.Tier1PassRate = tierPassRate(tierPass[1], tierTotal[1])
	metric.Tier2PassRate = tierPassRate(tierPass[2], tierTotal[2])
	metric.Tier3PassRate = tierPassRate(tierPass[3], tierTotal[3])
	metric.Tier4PassRate = tierPassRate(tierPass[4], tierTotal[4])

	catAvg := map[string]float64{}
	for cat, sum := range catScoreSum {
		if catCount[cat] > 0 {
			catAvg[cat] = sum / float64(catCount[cat])
		}
	}
	catJSON, _ := json.Marshal(catAvg)
	metric.CategoryScores = string(catJSON)

	return metric
}

func tierPassRate(pass, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(pass) / float64(total) * 100
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/tasks/... -run TestCalculateMetrics -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend/plugins/agentready/tasks/metrics_calculator.go backend/plugins/agentready/tasks/metrics_calculator_test.go
git commit -m "feat(agentready): add metrics calculator with tier pass rates and category scores

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 7: Plugin Entry Point + impl.go

**Files:**
- Create: `backend/plugins/agentready/agentready.go`
- Create: `backend/plugins/agentready/impl/impl.go`

- [ ] **Step 1: Create plugin entry point**

```go
// backend/plugins/agentready/agentready.go
package main

import (
	"github.com/apache/incubator-devlake/core/runner"
	"github.com/apache/incubator-devlake/plugins/agentready/impl"
	"github.com/spf13/cobra"
)

var PluginEntry impl.AgentReady

func main() {
	cmd := &cobra.Command{Use: "agentready"}
	projectName := cmd.Flags().StringP("project", "p", "", "project name to analyze")
	repoId := cmd.Flags().StringP("repoId", "r", "", "single repository domain ID")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		runner.DirectRun(cmd, args, PluginEntry, map[string]interface{}{
			"projectName": *projectName,
			"repoId":      *repoId,
		}, "")
	}
	runner.RunCmd(cmd)
}
```

- [ ] **Step 2: Create impl.go with all interface implementations**

```go
// backend/plugins/agentready/impl/impl.go
package impl

import (
	"encoding/json"

	"github.com/apache/incubator-devlake/core/context"
	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	coreModels "github.com/apache/incubator-devlake/core/models"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/agentready/api"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
	"github.com/apache/incubator-devlake/plugins/agentready/models/migrationscripts"
	"github.com/apache/incubator-devlake/plugins/agentready/tasks"
)

var _ interface {
	plugin.PluginMeta
	plugin.PluginInit
	plugin.PluginTask
	plugin.PluginModel
	plugin.PluginMetric
	plugin.PluginMigration
	plugin.PluginApi
	plugin.MetricPluginBlueprintV200
} = (*AgentReady)(nil)

type AgentReady struct{}

func (p AgentReady) Init(basicRes context.BasicRes) errors.Error {
	api.Init(basicRes, p)
	return nil
}

func (p AgentReady) Description() string {
	return "Collect and analyze AI readiness assessments from repositories"
}

func (p AgentReady) Name() string {
	return "agentready"
}

func (p AgentReady) RootPkgPath() string {
	return "github.com/apache/incubator-devlake/plugins/agentready"
}

func (p AgentReady) RequiredDataEntities() ([]map[string]interface{}, errors.Error) {
	return []map[string]interface{}{
		{
			"model": "repos",
			"requiredFields": map[string]string{
				"id":   "string",
				"name": "string",
			},
		},
	}, nil
}

func (p AgentReady) IsProjectMetric() bool {
	return true
}

func (p AgentReady) RunAfter() ([]string, errors.Error) {
	return []string{"github", "gitlab"}, nil
}

func (p AgentReady) Settings() interface{} {
	return nil
}

func (p AgentReady) GetTablesInfo() []dal.Tabler {
	return []dal.Tabler{
		&models.AgentReadyAssessment{},
		&models.AgentReadyFinding{},
		&models.AgentReadyMetric{},
		&models.AgentReadyScopeConfig{},
	}
}

func (p AgentReady) SubTaskMetas() []plugin.SubTaskMeta {
	return []plugin.SubTaskMeta{
		tasks.CollectAssessmentsMeta,
		tasks.ExtractAssessmentsMeta,
		tasks.CalculateMetricsMeta,
	}
}

func (p AgentReady) PrepareTaskData(taskCtx plugin.TaskContext, options map[string]interface{}) (interface{}, errors.Error) {
	logger := taskCtx.GetLogger()
	logger.Debug("Preparing AgentReady task data: %v", options)

	op, err := tasks.DecodeTaskOptions(options)
	if err != nil {
		return nil, err
	}

	err = tasks.ValidateTaskOptions(op)
	if err != nil {
		return nil, err
	}

	if op.ScopeConfig == nil && op.ScopeConfigId != 0 {
		var scopeConfig models.AgentReadyScopeConfig
		db := taskCtx.GetDal()
		dbErr := db.First(&scopeConfig, dal.Where("id = ?", op.ScopeConfigId))
		if dbErr != nil && !db.IsErrorNotFound(dbErr) {
			return nil, errors.BadInput.Wrap(dbErr, "failed to get scopeConfig")
		}
		op.ScopeConfig = &scopeConfig
	}

	if op.ScopeConfig == nil {
		op.ScopeConfig = models.GetDefaultScopeConfig()
	}

	return &tasks.AgentReadyTaskData{
		Options: op,
	}, nil
}

func (p AgentReady) ApiResources() map[string]map[string]plugin.ApiResourceHandler {
	return map[string]map[string]plugin.ApiResourceHandler{
		"assessments": {
			"GET": api.GetAssessments,
		},
		"assessments/:id": {
			"GET": api.GetAssessment,
		},
		"assessments/:id/findings": {
			"GET": api.GetAssessmentFindings,
		},
		"stats": {
			"GET": api.GetStats,
		},
		"scope-configs": {
			"GET":  api.GetScopeConfigs,
			"POST": api.CreateScopeConfig,
		},
		"scope-configs/:id": {
			"GET":    api.GetScopeConfig,
			"PATCH":  api.UpdateScopeConfig,
			"DELETE": api.DeleteScopeConfig,
		},
	}
}

func (p AgentReady) MigrationScripts() []plugin.MigrationScript {
	return migrationscripts.All()
}

func (p AgentReady) MakeMetricPluginPipelinePlanV200(projectName string, options json.RawMessage) (coreModels.PipelinePlan, errors.Error) {
	op := &tasks.AgentReadyOptions{}
	if options != nil && string(options) != "\"\"" {
		err := json.Unmarshal(options, op)
		if err != nil {
			return nil, errors.Default.WrapRaw(err)
		}
	}

	opts := map[string]interface{}{
		"projectName": projectName,
	}
	if op.ScopeConfigId != 0 {
		opts["scopeConfigId"] = op.ScopeConfigId
	}

	plan := coreModels.PipelinePlan{
		{
			{
				Plugin:  "agentready",
				Options: opts,
				Subtasks: []string{
					tasks.CollectAssessmentsMeta.Name,
					tasks.ExtractAssessmentsMeta.Name,
					tasks.CalculateMetricsMeta.Name,
				},
			},
		},
	}
	return plan, nil
}
```

- [ ] **Step 3: Verify plugin compiles**

Run: `cd /home/bramos/projects/devlake/backend && go build ./plugins/agentready/...`
Expected: Clean compile (will fail until API package exists — that's Task 8)

- [ ] **Step 4: Commit**

```bash
git add backend/plugins/agentready/agentready.go backend/plugins/agentready/impl/
git commit -m "feat(agentready): add plugin entry point and impl with all interfaces

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 8: API Layer

**Files:**
- Create: `backend/plugins/agentready/api/init.go`
- Create: `backend/plugins/agentready/api/assessments.go`
- Create: `backend/plugins/agentready/api/stats.go`
- Create: `backend/plugins/agentready/api/scope_config.go`

- [ ] **Step 1: Create API init**

```go
// backend/plugins/agentready/api/init.go
package api

import (
	"github.com/apache/incubator-devlake/core/context"
	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/plugin"
)

var db dal.Dal
var basicRes context.BasicRes

func Init(br context.BasicRes, _ plugin.PluginMeta) {
	basicRes = br
	db = basicRes.GetDal()
}
```

- [ ] **Step 2: Create assessments endpoint**

```go
// backend/plugins/agentready/api/assessments.go
package api

import (
	"net/http"
	"strconv"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

func GetAssessments(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	page, _ := strconv.Atoi(input.Query.Get("page"))
	if page <= 0 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(input.Query.Get("pageSize"))
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	var clauses []dal.Clause

	if projectName := input.Query.Get("projectName"); projectName != "" {
		clauses = []dal.Clause{
			dal.Select("a.*"),
			dal.From("_tool_agentready_assessments a"),
			dal.Join("JOIN project_mapping pm ON a.repo_id = pm.row_id"),
			dal.Where("pm.project_name = ? AND pm.`table` = ?", projectName, "repos"),
		}
	} else {
		clauses = []dal.Clause{
			dal.From(&models.AgentReadyAssessment{}),
		}
		if repoId := input.Query.Get("repoId"); repoId != "" {
			clauses = append(clauses, dal.Where("repo_id = ?", repoId))
		}
	}

	if cert := input.Query.Get("certification"); cert != "" {
		clauses = append(clauses, dal.Where("certification_level = ?", cert))
	}

	countClauses := make([]dal.Clause, len(clauses))
	copy(countClauses, clauses)
	total, err := db.Count(countClauses...)
	if err != nil {
		return nil, errors.Default.Wrap(err, "failed to count assessments")
	}

	clauses = append(clauses,
		dal.Orderby("assessed_at DESC"),
		dal.Limit(pageSize),
		dal.Offset(offset),
	)

	var assessments []models.AgentReadyAssessment
	err = db.All(&assessments, clauses...)
	if err != nil {
		return nil, errors.Default.Wrap(err, "failed to query assessments")
	}

	return &plugin.ApiResourceOutput{
		Body: map[string]interface{}{
			"assessments": assessments,
			"page":        page,
			"pageSize":    pageSize,
			"total":       total,
		},
		Status: http.StatusOK,
	}, nil
}

func GetAssessment(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	id := input.Params["id"]
	var assessment models.AgentReadyAssessment
	err := db.First(&assessment, dal.Where("id = ?", id))
	if err != nil {
		return nil, errors.Default.Wrap(err, "assessment not found")
	}

	var findings []models.AgentReadyFinding
	_ = db.All(&findings,
		dal.From(&models.AgentReadyFinding{}),
		dal.Where("assessment_id = ?", id),
		dal.Orderby("tier ASC, status ASC"),
	)

	return &plugin.ApiResourceOutput{
		Body: map[string]interface{}{
			"assessment": assessment,
			"findings":   findings,
		},
		Status: http.StatusOK,
	}, nil
}

func GetAssessmentFindings(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	assessmentId := input.Params["id"]

	clauses := []dal.Clause{
		dal.From(&models.AgentReadyFinding{}),
		dal.Where("assessment_id = ?", assessmentId),
	}

	if tier := input.Query.Get("tier"); tier != "" {
		clauses = append(clauses, dal.Where("tier = ?", tier))
	}
	if status := input.Query.Get("status"); status != "" {
		clauses = append(clauses, dal.Where("status = ?", status))
	}
	if category := input.Query.Get("category"); category != "" {
		clauses = append(clauses, dal.Where("category = ?", category))
	}

	clauses = append(clauses, dal.Orderby("tier ASC, status ASC"))

	var findings []models.AgentReadyFinding
	err := db.All(&findings, clauses...)
	if err != nil {
		return nil, errors.Default.Wrap(err, "failed to query findings")
	}

	return &plugin.ApiResourceOutput{
		Body:   findings,
		Status: http.StatusOK,
	}, nil
}
```

- [ ] **Step 3: Create stats endpoint**

```go
// backend/plugins/agentready/api/stats.go
package api

import (
	"net/http"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

func GetStats(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	var clauses []dal.Clause

	if projectName := input.Query.Get("projectName"); projectName != "" {
		clauses = []dal.Clause{
			dal.Select("a.*"),
			dal.From("_tool_agentready_assessments a"),
			dal.Join("JOIN project_mapping pm ON a.repo_id = pm.row_id"),
			dal.Where("pm.project_name = ? AND pm.`table` = ?", projectName, "repos"),
		}
	} else {
		clauses = []dal.Clause{
			dal.From(&models.AgentReadyAssessment{}),
		}
	}
	clauses = append(clauses, dal.Where("id != ''"))

	var assessments []models.AgentReadyAssessment
	err := db.All(&assessments, clauses...)
	if err != nil {
		return nil, errors.Default.Wrap(err, "failed to query assessments for stats")
	}

	certDist := map[string]int{}
	var totalScore float64
	for _, a := range assessments {
		certDist[a.CertificationLevel]++
		totalScore += a.OverallScore
	}

	avgScore := 0.0
	if len(assessments) > 0 {
		avgScore = totalScore / float64(len(assessments))
	}

	return &plugin.ApiResourceOutput{
		Body: map[string]interface{}{
			"totalRepos":              len(assessments),
			"averageScore":            avgScore,
			"certificationDistribution": certDist,
		},
		Status: http.StatusOK,
	}, nil
}
```

- [ ] **Step 4: Create scope config CRUD**

```go
// backend/plugins/agentready/api/scope_config.go
package api

import (
	"net/http"
	"strconv"

	"github.com/apache/incubator-devlake/core/dal"
	"github.com/apache/incubator-devlake/core/errors"
	"github.com/apache/incubator-devlake/core/plugin"
	helper "github.com/apache/incubator-devlake/helpers/pluginhelper/api"
	"github.com/apache/incubator-devlake/plugins/agentready/models"
)

func GetScopeConfigs(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	var configs []models.AgentReadyScopeConfig
	err := db.All(&configs, dal.From(&models.AgentReadyScopeConfig{}))
	if err != nil {
		return nil, errors.Default.Wrap(err, "failed to query scope configs")
	}
	return &plugin.ApiResourceOutput{
		Body:   configs,
		Status: http.StatusOK,
	}, nil
}

func CreateScopeConfig(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	var config models.AgentReadyScopeConfig
	err := helper.Decode(input.Body, &config, nil)
	if err != nil {
		return nil, errors.BadInput.Wrap(err, "failed to decode scope config")
	}
	if config.AssessmentFilePath == "" {
		config.AssessmentFilePath = models.DefaultAssessmentFilePath
	}
	dbErr := db.Create(&config)
	if dbErr != nil {
		return nil, errors.Default.Wrap(dbErr, "failed to create scope config")
	}
	return &plugin.ApiResourceOutput{
		Body:   config,
		Status: http.StatusCreated,
	}, nil
}

func GetScopeConfig(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	id, _ := strconv.ParseUint(input.Params["id"], 10, 64)
	var config models.AgentReadyScopeConfig
	err := db.First(&config, dal.Where("id = ?", id))
	if err != nil {
		return nil, errors.Default.Wrap(err, "scope config not found")
	}
	return &plugin.ApiResourceOutput{
		Body:   config,
		Status: http.StatusOK,
	}, nil
}

func UpdateScopeConfig(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	id, _ := strconv.ParseUint(input.Params["id"], 10, 64)
	var config models.AgentReadyScopeConfig
	err := db.First(&config, dal.Where("id = ?", id))
	if err != nil {
		return nil, errors.Default.Wrap(err, "scope config not found")
	}
	decodeErr := helper.Decode(input.Body, &config, nil)
	if decodeErr != nil {
		return nil, errors.BadInput.Wrap(decodeErr, "failed to decode update")
	}
	dbErr := db.Update(&config)
	if dbErr != nil {
		return nil, errors.Default.Wrap(dbErr, "failed to update scope config")
	}
	return &plugin.ApiResourceOutput{
		Body:   config,
		Status: http.StatusOK,
	}, nil
}

func DeleteScopeConfig(input *plugin.ApiResourceInput) (*plugin.ApiResourceOutput, errors.Error) {
	id, _ := strconv.ParseUint(input.Params["id"], 10, 64)
	err := db.Delete(&models.AgentReadyScopeConfig{}, dal.Where("id = ?", id))
	if err != nil {
		return nil, errors.Default.Wrap(err, "failed to delete scope config")
	}
	return &plugin.ApiResourceOutput{
		Status: http.StatusNoContent,
	}, nil
}
```

- [ ] **Step 5: Verify full plugin compiles**

Run: `cd /home/bramos/projects/devlake/backend && go build ./plugins/agentready/...`
Expected: Clean compile

- [ ] **Step 6: Commit**

```bash
git add backend/plugins/agentready/api/
git commit -m "feat(agentready): add REST API for assessments, stats, and scope configs

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 9: Run All Tests

- [ ] **Step 1: Run all unit tests**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/... -v`
Expected: All tests pass

- [ ] **Step 2: Run linter**

Run: `cd /home/bramos/projects/devlake/backend && golangci-lint run ./plugins/agentready/...`
Expected: No lint errors (fix any that appear)

- [ ] **Step 3: Run gofmt**

Run: `cd /home/bramos/projects/devlake/backend && gofmt -w ./plugins/agentready/`
Expected: Files formatted

- [ ] **Step 4: Commit any fixes**

```bash
git add backend/plugins/agentready/
git commit -m "fix(agentready): address lint and formatting issues

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 10: Grafana Dashboards

**Files:**
- Create: `backend/plugins/agentready/grafana/fleet-overview.json`
- Create: `backend/plugins/agentready/grafana/repo-detail.json`
- Create: `backend/plugins/agentready/grafana/findings-analysis.json`

- [ ] **Step 1: Create Fleet Overview dashboard**

Create `backend/plugins/agentready/grafana/fleet-overview.json` with these panels:

1. **Score by Repository** (Table panel):
```sql
SELECT
  a.repo_name,
  a.overall_score,
  a.certification_level,
  a.attributes_assessed,
  a.attributes_total,
  a.assessed_at
FROM _tool_agentready_assessments a
INNER JOIN (
  SELECT repo_id, MAX(assessed_at) as max_assessed
  FROM _tool_agentready_assessments
  WHERE id != ''
  GROUP BY repo_id
) latest ON a.repo_id = latest.repo_id AND a.assessed_at = latest.max_assessed
ORDER BY a.overall_score DESC
```

2. **Certification Distribution** (Pie chart):
```sql
SELECT
  certification_level AS metric,
  COUNT(*) AS value
FROM _tool_agentready_assessments a
INNER JOIN (
  SELECT repo_id, MAX(assessed_at) as max_assessed
  FROM _tool_agentready_assessments WHERE id != ''
  GROUP BY repo_id
) latest ON a.repo_id = latest.repo_id AND a.assessed_at = latest.max_assessed
GROUP BY certification_level
```

3. **Average Score Over Time** (Time series):
```sql
SELECT
  assessed_at AS time,
  AVG(overall_score) AS "Average Score"
FROM _tool_agentready_assessments
WHERE id != '' AND $__timeFilter(assessed_at)
GROUP BY DATE(assessed_at)
ORDER BY assessed_at
```

4. **Fleet Stats** (Stat panels):
```sql
-- Average Score
SELECT AVG(overall_score) AS "Avg Score"
FROM _tool_agentready_assessments a
INNER JOIN (
  SELECT repo_id, MAX(assessed_at) as max_assessed
  FROM _tool_agentready_assessments WHERE id != ''
  GROUP BY repo_id
) latest ON a.repo_id = latest.repo_id AND a.assessed_at = latest.max_assessed

-- Total Repos
SELECT COUNT(DISTINCT repo_id) AS "Total Repos"
FROM _tool_agentready_assessments WHERE id != ''
```

The full Grafana JSON structure should follow DevLake's existing dashboard conventions. Use `${datasource}` as the datasource variable. Create this as a standard Grafana dashboard JSON with panels array, templating for `datasource` variable, and time range defaults.

- [ ] **Step 2: Create Repository Detail dashboard**

Create `backend/plugins/agentready/grafana/repo-detail.json` with panels:

1. **Score History** (Time series):
```sql
SELECT assessed_at AS time, overall_score AS "Score"
FROM _tool_agentready_assessments
WHERE repo_id = '${repo_id}' AND id != '' AND $__timeFilter(assessed_at)
ORDER BY assessed_at
```

2. **Findings by Tier** (Bar chart):
```sql
SELECT
  CASE tier WHEN 1 THEN 'Essential' WHEN 2 THEN 'Critical' WHEN 3 THEN 'Important' WHEN 4 THEN 'Advanced' END AS tier_name,
  SUM(CASE WHEN status = 'pass' THEN 1 ELSE 0 END) AS "Pass",
  SUM(CASE WHEN status = 'fail' THEN 1 ELSE 0 END) AS "Fail"
FROM _tool_agentready_findings f
JOIN _tool_agentready_assessments a ON f.assessment_id = a.id
WHERE a.repo_id = '${repo_id}'
  AND a.assessed_at = (SELECT MAX(assessed_at) FROM _tool_agentready_assessments WHERE repo_id = '${repo_id}' AND id != '')
GROUP BY tier
ORDER BY tier
```

3. **Finding Details Table** (Table):
```sql
SELECT
  f.attribute_name,
  f.category,
  f.tier,
  f.status,
  f.score,
  f.measured_value,
  f.remediation_summary
FROM _tool_agentready_findings f
JOIN _tool_agentready_assessments a ON f.assessment_id = a.id
WHERE a.repo_id = '${repo_id}'
  AND a.assessed_at = (SELECT MAX(assessed_at) FROM _tool_agentready_assessments WHERE repo_id = '${repo_id}' AND id != '')
ORDER BY f.tier, f.status DESC
```

Template variables: `$repo_id` (query: `SELECT DISTINCT repo_id FROM _tool_agentready_assessments WHERE id != ''`)

- [ ] **Step 3: Create Findings Analysis dashboard**

Create `backend/plugins/agentready/grafana/findings-analysis.json` with panels:

1. **Most Common Failures** (Bar chart):
```sql
SELECT
  f.attribute_name,
  COUNT(*) AS failure_count
FROM _tool_agentready_findings f
WHERE f.status = 'fail'
GROUP BY f.attribute_id, f.attribute_name
ORDER BY failure_count DESC
LIMIT 15
```

2. **Tier Pass Rates Over Time** (Time series):
```sql
SELECT
  m.assessed_at AS time,
  AVG(m.tier1_pass_rate) AS "Essential",
  AVG(m.tier2_pass_rate) AS "Critical",
  AVG(m.tier3_pass_rate) AS "Important",
  AVG(m.tier4_pass_rate) AS "Advanced"
FROM _tool_agentready_metrics m
WHERE $__timeFilter(m.assessed_at)
GROUP BY DATE(m.assessed_at)
ORDER BY m.assessed_at
```

3. **Remediation Backlog** (Table):
```sql
SELECT
  f.attribute_name,
  f.category,
  f.tier,
  f.default_weight,
  COUNT(DISTINCT f.repo_id) AS affected_repos,
  f.remediation_summary
FROM _tool_agentready_findings f
WHERE f.status = 'fail'
GROUP BY f.attribute_id, f.attribute_name, f.category, f.tier, f.default_weight, f.remediation_summary
ORDER BY f.tier ASC, f.default_weight DESC
```

Note: Generate valid Grafana dashboard JSON. Each dashboard should have `__inputs` for datasource, `templating` for variables, and standard `panels` array with `gridPos` layout. Follow the pattern from existing DevLake Grafana dashboards if available in the repo, or use standard Grafana 9+ JSON format.

- [ ] **Step 4: Commit**

```bash
git add backend/plugins/agentready/grafana/
git commit -m "feat(agentready): add Grafana dashboards for fleet overview, repo detail, and findings analysis

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

---

## Task 11: Final Verification

- [ ] **Step 1: Full build**

Run: `cd /home/bramos/projects/devlake/backend && go build ./plugins/agentready/...`
Expected: Clean compile

- [ ] **Step 2: Full test suite**

Run: `cd /home/bramos/projects/devlake/backend && go test ./plugins/agentready/... -v -count=1`
Expected: All tests pass

- [ ] **Step 3: Lint**

Run: `cd /home/bramos/projects/devlake/backend && golangci-lint run ./plugins/agentready/...`
Expected: Clean

- [ ] **Step 4: Verify file structure**

Run: `find /home/bramos/projects/devlake/backend/plugins/agentready -type f | sort`
Expected: Matches the file map from the top of this plan

- [ ] **Step 5: Final commit if needed**

```bash
git add backend/plugins/agentready/
git commit -m "chore(agentready): final cleanup and verification

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

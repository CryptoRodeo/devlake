# DevLake & AgentReady Plugin — A Developer's Tour

A hands-on guide for a mid-level engineer who knows some Go but is new to DevLake. By the end, you'll understand how DevLake works, how plugins fit in, and how the `agentready` plugin collects, processes, and stores data.

---

## Table of Contents

1. [Go Refresher for DevLake](#1-go-refresher-for-devlake)
2. [What Is DevLake?](#2-what-is-devlake)
3. [Architecture Overview](#3-architecture-overview)
4. [The Plugin System](#4-the-plugin-system)
5. [The Data Access Layer (DAL)](#5-the-data-access-layer-dal)
6. [The Task Pipeline](#6-the-task-pipeline)
7. [Helper Packages You'll Use](#7-helper-packages-youll-use)
8. [The AgentReady Plugin — Deep Dive](#8-the-agentready-plugin--deep-dive)
9. [How AgentReady Fits Into DevLake](#9-how-agentready-fits-into-devlake)
10. [Testing Your Plugin](#10-testing-your-plugin)
11. [Running & Debugging Locally](#11-running--debugging-locally)
12. [Key Files Reference](#12-key-files-reference)

---

## 1. Go Refresher for DevLake

DevLake uses several Go patterns heavily. Here's a quick refresher on the ones that matter most.

### Interfaces

Go interfaces are satisfied *implicitly* — no `implements` keyword. If your struct has the right methods, it satisfies the interface:

```go
// DevLake defines this interface
type PluginMeta interface {
    Name() string
    Description() string
    RootPkgPath() string
}

// Your plugin satisfies it by implementing those methods
type AgentReady struct{}

func (p AgentReady) Name() string        { return "agentready" }
func (p AgentReady) Description() string { return "AI readiness assessments" }
func (p AgentReady) RootPkgPath() string { return "github.com/..." }
```

This is how DevLake's plugin system works — it checks at runtime which interfaces your plugin satisfies and enables features accordingly.

### Struct Embedding

Go uses composition over inheritance. You embed one struct inside another:

```go
type NoPKModel struct {
    CreatedAt time.Time
    UpdatedAt time.Time
}

type AgentReadyAssessment struct {
    NoPKModel              // Embedded — gets CreatedAt/UpdatedAt for free
    Id          string     // Your own fields
    RepoId      string
    OverallScore float64
}
```

DevLake models embed `common.NoPKModel` or `common.Model` to get standard fields.

### Pointers and Nil

Go distinguishes between zero values and "no value." DevLake uses pointers for optional fields:

```go
type AgentReadyFinding struct {
    Score *float64  // nil = "no score provided", 0.0 = "scored zero"
}
```

When you see `*float64`, think "this field might be absent."

### Error Handling

Go doesn't have exceptions. Every function that can fail returns an `error`:

```go
result, err := doSomething()
if err != nil {
    return fmt.Errorf("doing something for repo %s: %w", repoId, err)
}
```

The `%w` verb *wraps* the error — callers can unwrap it later to check the original cause. DevLake expects you to propagate errors up, not log-and-swallow them.

### Struct Tags

Go struct tags control serialization and ORM behavior. DevLake uses them heavily:

```go
type Assessment struct {
    Id           string  `json:"id" gorm:"primaryKey;type:varchar(255)"`
    RepoId       string  `json:"repo_id" gorm:"index;type:varchar(255)"`
    OverallScore float64 `json:"overall_score" gorm:"type:double"`
}
```

- `json:"..."` — JSON field names (for API responses)
- `gorm:"..."` — database column config (primary keys, indexes, types)

### Type Assertions

When DevLake passes data through `interface{}`, you need to assert the concrete type:

```go
func MySubtask(ctx plugin.SubTaskContext) errors.Error {
    taskData := ctx.TaskContext().GetData().(*AgentReadyTaskData)
    // taskData is now the concrete type you set in PrepareTaskData
}
```

The `.(Type)` syntax panics if the assertion is wrong. Use `value, ok := x.(Type)` for safe assertions.

---

## 2. What Is DevLake?

Apache DevLake is an open-source **data platform** for software engineering teams. It:

1. **Ingests** data from 40+ DevOps tools (GitHub, GitLab, Jira, Jenkins, SonarQube, etc.)
2. **Transforms** fragmented tool data into a standardized domain model
3. **Visualizes** insights via integrated Grafana dashboards

Primary use case: implement DORA metrics (Deployment Frequency, Lead Time, Change Failure Rate, MTTR) and understand development lifecycle across tools.

Think of it as an ETL pipeline for developer tools:

```
GitHub API ─┐
GitLab API ─┤──→ DevLake ──→ Normalized DB ──→ Grafana Dashboards
Jira API   ─┤
Jenkins    ─┘
```

Each data source is a **plugin**. Your `agentready` plugin is one of these — it collects AI-readiness assessment data from repos.

---

## 3. Architecture Overview

### System Architecture

```
┌──────────────────────────────────────────────┐
│           Config-UI (React + Antd)           │
│     Blueprint editor, connection setup,      │
│     pipeline monitor, scope selection        │
└──────────────────┬───────────────────────────┘
                   │ REST API
                   ▼
┌──────────────────────────────────────────────┐
│          Backend (Go, Gin framework)         │
│                                              │
│  ┌─────────┐  ┌──────────┐  ┌────────────┐  │
│  │ API     │  │ Services │  │ Runner     │  │
│  │ Layer   │→ │ Layer    │→ │ (Pipeline/ │  │
│  │ (Gin)  │  │          │  │  Task)     │  │
│  └─────────┘  └──────────┘  └─────┬──────┘  │
│                                    │         │
│  ┌─────────────────────────────────▼──────┐  │
│  │         Plugin System                  │  │
│  │  github, jira, jenkins, agentready, ...│  │
│  └─────────────────────────────────┬──────┘  │
│                                    │         │
│  ┌─────────────────────────────────▼──────┐  │
│  │    DAL (Data Access Layer) → MySQL     │  │
│  └────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
                   │
                   ▼
┌──────────────────────────────────────────────┐
│              Grafana Dashboards              │
│         (queries domain layer tables)        │
└──────────────────────────────────────────────┘
```

### Three Data Layers

DevLake organizes data in three layers:

```
┌────────────────────────────────────────────────┐
│  Tool Layer (Raw Data)                         │
│  Tables: _tool_github_repos, _tool_gitlab_*    │
│  What: Raw API data, provider-specific schemas  │
└────────────────┬───────────────────────────────┘
                 │ Convert/Extract
┌────────────────▼───────────────────────────────┐
│  Domain Layer (Normalized)                      │
│  Tables: repos, commits, pull_requests          │
│  What: Standardized schema across all providers │
└────────────────┬───────────────────────────────┘
                 │ Calculate
┌────────────────▼───────────────────────────────┐
│  Metric Layer (Aggregated)                      │
│  Tables: _tool_agentready_metrics               │
│  What: Calculated scores, dashboards, reports   │
└─────────────────────────────────────────────────┘
```

**Why this matters**: DORA metrics work identically whether your data comes from GitHub, GitLab, or Bitbucket. Dashboards query domain tables, not tool tables.

```
GithubIssue  ──┐
JiraIssue    ──┼──→  Issue (domain layer)  ──→  Grafana Dashboard
TapdBug      ──┘
```

**Your agentready plugin** operates across all three:
- **Tool Layer**: `_tool_agentready_assessments` (raw JSON from repos)
- **Domain-ish Layer**: `_tool_agentready_findings` (parsed assessment results)
- **Metric Layer**: `_tool_agentready_metrics` (aggregated pass rates)

### Backend Directory Structure

```
backend/
├── core/                  # Framework interfaces & contracts
│   ├── plugin/            # Plugin interfaces (PluginMeta, PluginTask, etc.)
│   ├── dal/               # Database abstraction layer
│   ├── models/            # Shared model types (Task, Pipeline, etc.)
│   └── runner/            # Task execution engine
├── helpers/
│   └── pluginhelper/api/  # Helpers for plugin development
├── impls/                 # Framework implementations
│   └── context/           # TaskContext, SubTaskContext implementations
├── plugins/               # All plugins live here
│   ├── github/
│   ├── gitlab/
│   ├── agentready/        # ← Your plugin
│   └── ...
└── server/                # HTTP server, API routes, service layer
```

---

## 4. The Plugin System

### How Plugins Get Loaded

When DevLake starts:

1. It scans `PLUGIN_DIR` for `.so` (shared object) files
2. Opens each `.so` and looks for a `PluginEntry` symbol
3. Checks which interfaces that symbol implements
4. Registers the plugin in a global map

```
LoadGoPlugins() → for each .so file:
    open → lookup "PluginEntry" → RegisterPlugin(name, entry)
```

The plugin hub (`backend/core/plugin/hub.go`) maintains a registry:

```go
RegisterPlugin(name string, plugin PluginMeta)
GetPlugin(name string) (PluginMeta, error)
AllPlugins() map[string]PluginMeta
```

### Plugin Interfaces (Mix and Match)

Plugins implement only the interfaces they need:

| Interface | Purpose | Required? |
|-----------|---------|-----------|
| `PluginMeta` | Name, description, root package | Yes |
| `PluginInit` | Initialization logic | No |
| `PluginTask` | Define subtasks for data processing | For data plugins |
| `PluginSource` | Define connections and scopes | For data source plugins |
| `PluginModel` | Declare database tables | If plugin has tables |
| `PluginMigration` | Database schema migrations | If plugin has tables |
| `PluginApi` | REST API endpoints | If plugin exposes API |
| `PluginMetric` | Metric calculation config | For metric plugins |
| `DataSourcePluginBlueprintV200` | Generate pipeline plans | For data source plugins |
| `MetricPluginBlueprintV200` | Generate metric plans | For metric plugins |

### The Interface Pattern in Practice

Here's how DevLake discovers your plugin's capabilities at runtime:

```go
// Somewhere in DevLake's core:
if taskPlugin, ok := pluginEntry.(PluginTask); ok {
    // This plugin has subtasks — run them
    subtasks := taskPlugin.SubTaskMetas()
}

if apiPlugin, ok := pluginEntry.(PluginApi); ok {
    // This plugin has API endpoints — register them
    resources := apiPlugin.ApiResources()
}
```

Your plugin opts into features by implementing interfaces. No config files, no annotations — just methods.

### Key Interfaces in Detail

**PluginMeta** — minimal required interface:

```go
// backend/core/plugin/plugin_meta.go
type PluginMeta interface {
    Name() string
    Description() string
    RootPkgPath() string
}
```

**PluginTask** — defines what work the plugin does:

```go
// backend/core/plugin/plugin_task.go
type PluginTask interface {
    SubTaskMetas() []SubTaskMeta
    PrepareTaskData(taskCtx TaskContext, options map[string]interface{})
}

type SubTaskMeta struct {
    Name             string
    EntryPoint       SubTaskEntryPoint  // the actual function
    Required         bool
    EnabledByDefault bool
    DomainTypes      []string           // CODE, TICKET, CODEREVIEW, etc.
    Dependencies     []*SubTaskMeta
    DependencyTables []string           // input tables this subtask reads
    ProductTables    []string           // output tables this subtask writes
}
```

**PluginSource** — for tools that connect to external services:

```go
// backend/core/plugin/plugin_datasource.go
type PluginSource interface {
    Connection() dal.Tabler      // connection config table
    Scope() ToolLayerScope       // what to collect (repo, project, etc.)
    ScopeConfig() dal.Tabler     // scope-specific settings
}
```

### Plugin Categories

**Data Source Plugins** (fetch from external tools):
- `github`, `gitlab`, `bitbucket` — code hosting
- `jira`, `tapd` — issue tracking
- `jenkins`, `circleci` — CI/CD
- `sonarqube` — code quality

**Metric Plugins** (compute metrics from domain data):
- `dora` — DORA metrics
- `linker` — links PRs to issues
- `refdiff` — calculates ref diffs
- `agentready` — AI readiness assessments

### Example Plugin Structures

**Simple metric plugin (Linker)**:
```
backend/plugins/linker/
├── linker.go              # PluginEntry (registration)
├── impl/
│   └── impl.go            # Implements interfaces
├── tasks/
│   └── link_pr_to_issue.go # SubTask implementation
└── models/
    └── migrationscripts/   # Database migrations
```

**Full data source plugin (GitHub)**:
```
backend/plugins/github/
├── github.go              # PluginEntry
├── impl/
│   └── impl.go            # 20+ subtasks, connection/scope definitions
├── models/
│   ├── connection.go      # GithubConnection
│   ├── repo.go            # GithubRepo (scope)
│   ├── issue.go           # GithubIssue (tool layer)
│   └── migrationscripts/
├── tasks/
│   ├── issue_collector.go
│   ├── issue_extractor.go
│   ├── issue_convertor.go # tool layer → domain layer
│   └── ...
├── api/                   # REST endpoints
└── e2e/                   # End-to-end tests
```

---

## 5. The Data Access Layer (DAL)

DevLake wraps GORM (a Go ORM) behind the `dal.Dal` interface. You'll interact with it through `SubTaskContext`:

```go
func MySubtask(ctx plugin.SubTaskContext) errors.Error {
    db := ctx.GetDal()  // Get the DAL instance

    // Query one record
    assessment := &Assessment{}
    err := db.First(assessment, dal.Where("id = ?", someId))

    // Query multiple records
    var findings []Finding
    err := db.All(&findings, dal.Where("assessment_id = ?", id))

    // Count
    count, err := db.Count(dal.From(&Assessment{}), dal.Where("repo_id = ?", repoId))

    // Insert or update
    err := db.CreateOrUpdate(myRecord)

    // Raw SQL (escape hatch)
    err := db.Exec("DELETE FROM _tool_agentready_metrics WHERE repo_id = ?", repoId)
}
```

### Common Query Clauses

```go
dal.From(&MyModel{})              // FROM _tool_my_table
dal.Where("col = ?", value)       // WHERE col = 'value'
dal.Orderby("created_at DESC")    // ORDER BY created_at DESC
dal.Limit(50)                     // LIMIT 50
dal.Offset(100)                   // OFFSET 100
dal.Join("JOIN other ON ...")      // JOIN
dal.Select("col1, col2")          // SELECT col1, col2
```

### Defining Models (Tables)

Every model implements `dal.Tabler`:

```go
type AgentReadyAssessment struct {
    Id           string    `json:"id" gorm:"primaryKey;type:varchar(255)"`
    RepoId       string    `json:"repo_id" gorm:"index;type:varchar(255)"`
    OverallScore float64   `json:"overall_score" gorm:"type:double"`
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
}

func (AgentReadyAssessment) TableName() string {
    return "_tool_agentready_assessments"
}
```

Convention: tool-layer tables use `_tool_<plugin>_<entity>` naming.

### Migrations

Each plugin manages its own schema via `MigrationScript`:

```go
type MigrationScript interface {
    Up(basicRes context.BasicRes) errors.Error
    Version() uint64   // timestamp-based version (e.g., 20260511000001)
    Name() string
}
```

Migrations run automatically at startup. DevLake tracks which have run in `_devlake_migration_history`.

---

## 6. The Task Pipeline

### Execution Hierarchy

```
Blueprint (what to collect, when)
    │
    ▼
Pipeline (one execution of a blueprint)
    │
    ▼
Stages (execute sequentially)
    │
    ├── Stage 1: [Task A, Task B]  ← parallel within stage
    ├── Stage 2: [Task C]          ← waits for stage 1
    └── Stage 3: [Task D, Task E]  ← waits for stage 2
         │
         ▼
    Each Task runs SubTasks sequentially:
         SubTask 1: CollectAssessments
         SubTask 2: ExtractAssessments
         SubTask 3: CalculateMetrics
```

When DevLake runs your plugin, here's the execution flow:

```
1. PrepareTaskData() → creates shared state for all subtasks
2. SubTask 1: CollectAssessments()   ← fetch from APIs
3. SubTask 2: ExtractAssessments()   ← parse JSON
4. SubTask 3: CalculateMetrics()     ← aggregate scores
5. Close() → cleanup (if implemented)
```

### SubTaskMeta — Declaring Your Subtasks

Each subtask is described by a `SubTaskMeta`:

```go
var CollectAssessmentsMeta = plugin.SubTaskMeta{
    Name:             "CollectAssessments",
    EntryPoint:       CollectAssessments,        // The function to run
    EnabledByDefault: true,
    Required:         false,
    Description:      "Collect assessment files from repositories",
    DependencyTables: []string{},                // Input tables
    ProductTables:    []string{"_tool_agentready_assessments"}, // Output tables
}
```

### PrepareTaskData — Setting Up Shared State

Before subtasks run, DevLake calls `PrepareTaskData`. You return a struct that all subtasks can access:

```go
func (p AgentReady) PrepareTaskData(
    taskCtx plugin.TaskContext,
    options map[string]interface{},
) (interface{}, error) {
    var opts AgentReadyOptions
    // Decode options from the pipeline config
    // Load scope config from DB
    // Set up any shared resources
    return &AgentReadyTaskData{Options: &opts}, nil
}
```

Subtasks retrieve this via type assertion:

```go
taskData := ctx.TaskContext().GetData().(*AgentReadyTaskData)
```

### Progress Reporting

Subtasks should report progress so the UI can show status:

```go
func CollectAssessments(ctx plugin.SubTaskContext) errors.Error {
    ctx.SetProgress(0, totalRepos)
    for _, repo := range repos {
        // ... do work ...
        ctx.IncProgress(1)
    }
    return nil
}
```

### Context Hierarchy

DevLake provides a three-level context system:

```
BasicRes (config, logger, database)
  └── ExecContext (name, context, data, progress)
      └── TaskContext (task data, sync policy)
          └── SubTaskContext (subtask execution)
```

Key methods available at every level:
- `GetDal()` — database access
- `GetLogger()` — structured logging
- `GetContext()` — Go `context.Context` for cancellation
- `GetConfigReader()` — config values

---

## 7. Helper Packages You'll Use

DevLake provides utilities in `backend/helpers/pluginhelper/api/`:

| Package | What It Does |
|---------|-------------|
| `api.ApiClient` | HTTP client with auth, rate limiting, retries |
| `api.BatchSave` | Efficient batch database inserts |
| `api.ApiCollector` | Paginated API collection with cursor support |
| `api.ApiExtractor` | Extract structured data from raw API responses |
| `api.DataConvertor` | Transform raw → domain models |
| `api.ConnectionHelper` | Load connection configs from DB |
| `api.ScopeHelper` | Manage scopes (repos, projects) |

### ApiClient Example

```go
client, err := helper.NewApiClient(
    ctx, conn.Endpoint, nil, 0, conn.Proxy, basicRes,
)
client.SetHeaders(map[string]string{
    "Authorization": fmt.Sprintf("Bearer %s", conn.Token),
})

resp, err := client.Get(
    "repos/owner/repo/contents/.agentready/assessment.json",
    nil, nil,
)
```

The agentready plugin does HTTP calls directly (via `net/http`) rather than using `ApiClient`, which is fine for simpler use cases where you don't need rate limiting or pagination.

---

## 8. The AgentReady Plugin — Deep Dive

### What It Does

AgentReady assesses how "AI-ready" a repository is — whether it has good documentation, security practices, testing, etc. for AI-assisted development.

The workflow:
1. Repos contain a JSON assessment file (e.g., `.agentready/assessment-latest.json`)
2. The plugin fetches these files from GitHub/GitLab APIs
3. Parses them into structured assessment + finding records
4. Calculates aggregate metrics (pass rates by tier, category scores)
5. Serves the data via REST API and Grafana dashboards

### Directory Layout

```
backend/plugins/agentready/
├── agentready.go              # CLI entry point (for standalone runs)
├── impl/
│   └── impl.go                # Plugin registration (all interfaces)
├── models/
│   ├── assessment.go          # AgentReadyAssessment model
│   ├── finding.go             # AgentReadyFinding model
│   ├── metric.go              # AgentReadyMetric model
│   ├── scope_config.go        # AgentReadyScopeConfig model
│   └── migrationscripts/
│       ├── init_schema.go     # CREATE TABLE migrations
│       └── register.go        # Migration registry
├── tasks/
│   ├── task_data.go           # Shared task data & options
│   ├── assessment_collector.go     # Stage 1: Fetch from APIs
│   ├── assessment_extractor.go     # Stage 2: Parse JSON
│   ├── metrics_calculator.go       # Stage 3: Aggregate metrics
│   └── *_test.go                   # Unit tests for each stage
├── api/
│   ├── init.go                # API initialization
│   ├── assessments.go         # GET /assessments, GET /assessments/:id
│   ├── stats.go               # GET /stats
│   └── scope_config.go        # CRUD for scope configs
└── grafana/                   # Dashboard JSON definitions
    ├── fleet-overview.json
    ├── findings-analysis.json
    └── repo-detail.json
```

### The Four Models

#### Assessment — The Core Record

```go
type AgentReadyAssessment struct {
    Id                 string    // Composite key: "repoId:commitHash"
    RepoId             string    // Domain repo ID (indexed)
    RepoName           string
    ConnectionId       uint64    // Links to GitHub/GitLab connection
    Provider           string    // "github" or "gitlab"
    SchemaVersion      string
    OverallScore       float64   // 0-100
    CertificationLevel string   // Platinum|Gold|Silver|Bronze|NeedsImprovement|None
    AttributesAssessed int
    AttributesTotal    int
    Branch             string
    CommitHash         string
    DurationSeconds    float64
    AssessedAt         time.Time // When the assessment ran (indexed)
    CollectedAt        time.Time // When DevLake collected it
    RawJSON            string    // Original JSON from repo
}
```

**Key design choice**: The `Id` is a composite key `repoId:commitHash`. This means each commit produces a unique assessment — you can track how a repo's readiness evolves over time.

#### Finding — Individual Check Results

```go
type AgentReadyFinding struct {
    Id                 string    // Composite key: "assessmentId:attributeId"
    AssessmentId       string    // FK to assessment (indexed)
    RepoId             string    // (indexed)
    AttributeId        string    // e.g., "doc-readme-exists"
    AttributeName      string    // e.g., "README Exists"
    Category           string    // e.g., "Documentation Standards"
    Tier               int       // 1=Essential, 2=Critical, 3=Important, 4=Advanced
    Status             string    // pass|fail|skipped|error
    Score              *float64  // Nullable — some checks are binary pass/fail
    MeasuredValue      string    // Actual measured value
    Threshold          string    // Expected threshold
    Evidence           string    // JSON array of evidence strings
    RemediationSummary string    // Short fix description
    RemediationSteps   string    // JSON array of fix steps
    DefaultWeight      float64
}
```

**Tier system**: Tier 1 = must-have basics, Tier 4 = advanced nice-to-haves. Dashboards show "fix the essentials first."

**Status filtering**: `not_applicable` findings are excluded before storage — they'd skew metrics.

#### Metric — Aggregated Scores

```go
type AgentReadyMetric struct {
    Id             string    // "repoId:20260510T143000"
    RepoId         string    // (indexed)
    AssessedAt     time.Time // (indexed)
    PassCount      int
    FailCount      int
    SkipCount      int
    Tier1PassRate  float64   // 0-100
    Tier2PassRate  float64
    Tier3PassRate  float64
    Tier4PassRate  float64
    CategoryScores string   // JSON: {"Security": 85.5, "Docs": 92.0}
}
```

This table exists for dashboard performance — querying raw findings for every dashboard load would be slow.

#### ScopeConfig — Plugin Configuration

```go
type AgentReadyScopeConfig struct {
    common.ScopeConfig                    // Inherits Id, ConnectionId, etc.
    Branch              string            // Branch to check (optional)
    AssessmentFilePath  string            // Default: ".agentready/assessment-latest.json"
    ExcludeRepos        string            // Comma-separated exclusion list
}
```

### The Three-Stage Pipeline

#### Stage 1: Collector — Fetching Raw Data

**File**: `tasks/assessment_collector.go`

```
Input:  ProjectName or RepoId from pipeline options
Output: AgentReadyAssessment records with RawJSON populated

Flow:
1. Resolve which repos to process
   ├── Single repo: parse domain ID directly
   └── Project: query project_mapping table for all repos

2. For each repo, look up its provider connection (GitHub or GitLab)

3. Fetch the assessment file via provider API:
   ├── GitHub: GET /repos/{owner}/{repo}/contents/{path} → base64 decode
   └── GitLab: GET /api/v4/projects/{id}/repository/files/{path}/raw

4. Handle 404 gracefully (no assessment file = skip, not error)

5. Store raw JSON in AgentReadyAssessment.RawJSON
```

**Domain Repo ID format**: `github:GithubRepo:1:12345`
```
         │         │       │   │
         │         │       │   └── Scope ID (repo ID in that connection)
         │         │       └────── Connection ID
         │         └────────────── Model type
         └──────────────────────── Provider
```

This is how DevLake links cross-plugin data. The `project_mapping` table maps project names to domain repo IDs.

#### Stage 2: Extractor — Parsing JSON

**File**: `tasks/assessment_extractor.go`

```
Input:  AgentReadyAssessment records with RawJSON
Output: Updated assessments (parsed fields) + AgentReadyFinding records

Flow:
1. For each assessment with non-empty RawJSON:
2. Deserialize JSON into structured Go types
3. Populate assessment fields: score, certification, timestamps, etc.
4. For each finding in the JSON:
   ├── Skip if status == "not_applicable"
   ├── Generate finding ID: "assessmentId:attributeId"
   ├── Serialize evidence/remediation arrays as JSON strings
   └── Store as AgentReadyFinding record
```

**Assessment JSON schema** (what the plugin expects in repos):

```json
{
  "schema_version": "1.0.0",
  "repository": {
    "name": "my-repo",
    "branch": "main",
    "commit_hash": "abc123"
  },
  "timestamp": "2026-05-10T14:30:00Z",
  "overall_score": 85.5,
  "certification_level": "Gold",
  "attributes_assessed": 20,
  "attributes_total": 25,
  "findings": [
    {
      "attribute": {
        "id": "doc-readme-exists",
        "name": "README Exists",
        "category": "Documentation Standards",
        "tier": 1,
        "default_weight": 0.8
      },
      "status": "pass",
      "score": 100.0,
      "measured_value": "README.md found",
      "threshold": "File must exist",
      "evidence": ["Found README.md at root"],
      "remediation": {
        "summary": "Add a README",
        "steps": ["Create README.md", "Add project description"]
      }
    }
  ]
}
```

#### Stage 3: Calculator — Aggregating Metrics

**File**: `tasks/metrics_calculator.go`

```
Input:  AgentReadyAssessment + AgentReadyFinding records
Output: AgentReadyMetric records

Calculation logic:
1. For each assessment, fetch all its findings
2. Count: passes, failures, skips
3. Per tier (1-4):
   └── Tier Pass Rate = (passes in tier) / (total in tier) × 100
4. Per category:
   └── Category Score = average of all scores in that category
5. Generate Metric ID: "repoId:assessedAt.Format('20060102T150405')"
6. Store metric record
```

### API Endpoints

All registered under `/plugins/agentready/`:

| Method | Path | What It Does |
|--------|------|-------------|
| GET | `/assessments` | List assessments (paginated, filterable by project/repo/certification) |
| GET | `/assessments/:id` | One assessment + all its findings |
| GET | `/assessments/:id/findings` | Findings with tier/status/category filters |
| GET | `/stats` | Aggregate stats: total repos, avg score, certification distribution |
| GET | `/scope-configs` | List scope configs |
| POST | `/scope-configs` | Create scope config |
| GET | `/scope-configs/:id` | Get one scope config |
| PATCH | `/scope-configs/:id` | Update scope config |
| DELETE | `/scope-configs/:id` | Delete scope config |

### Grafana Dashboards

Three JSON dashboard definitions in `grafana/`:
- **fleet-overview.json** — Bird's-eye view across all repos
- **findings-analysis.json** — Drill into specific findings/categories
- **repo-detail.json** — Single repo deep dive

---

## 9. How AgentReady Fits Into DevLake

### It's a Metric Plugin

Unlike GitHub/GitLab (data-source plugins), agentready is a **metric plugin**:

```go
func (p AgentReady) IsProjectMetric() bool { return true }
func (p AgentReady) RunAfter() []string    { return []string{"github", "gitlab"} }
```

This means:
- It runs *after* GitHub/GitLab plugins collect repo data
- It reads from their tables (`_tool_github_repos`, `_tool_gitlab_projects`)
- It uses their connections (tokens, endpoints) to fetch assessment files
- It's triggered per-project, not per-connection

### Data Dependencies

```
GitHub Plugin                    AgentReady Plugin
┌─────────────────┐             ┌──────────────────────────┐
│ _tool_github_   │             │ _tool_agentready_        │
│   repos         │──reads──→  │   assessments             │
│   connections   │             │   findings                │
└─────────────────┘             │   metrics                 │
                                │   scope_configs           │
GitLab Plugin                   └──────────────────────────┘
┌─────────────────┐                      │
│ _tool_gitlab_   │──reads──→           │
│   projects      │                      ▼
│   connections   │             Grafana Dashboards
└─────────────────┘
        │
        └── project_mapping ──→ Links projects to repos
```

### Pipeline Plan Generation

When a project blueprint includes agentready, `MakeMetricPluginPipelinePlanV200()` generates a three-stage plan:

```go
// Stage 1: Collect (fetch assessment files from APIs)
// Stage 2: Extract (parse JSON into records)
// Stage 3: Calculate (aggregate metrics)
plan := models.PipelinePlan{
    {Task{Plugin: "agentready", Subtasks: ["CollectAssessments"], Options: opts}},
    {Task{Plugin: "agentready", Subtasks: ["ExtractAssessments"], Options: opts}},
    {Task{Plugin: "agentready", Subtasks: ["CalculateMetrics"], Options: opts}},
}
```

### End-to-End Request Flow

1. **User** opens Config-UI, enters GitHub credentials → backend saves `GithubConnection`
2. **User** selects repos, creates blueprint with agentready enabled
3. **Scheduler** fires cron → creates `Pipeline` from blueprint
4. **Runner** executes stages:
   - Stage 1: GitHub plugin collects repos → tool tables → domain tables
   - Stage 2: AgentReady collector fetches `.agentready/assessment-latest.json` from each repo
   - Stage 3: AgentReady extractor parses JSON → findings
   - Stage 4: AgentReady calculator aggregates → metrics
5. **Grafana** queries `_tool_agentready_*` tables → shows AI readiness dashboards

---

## 10. Testing Your Plugin

### Running Tests

```bash
cd backend

# All agentready tests
go test ./plugins/agentready/... -v

# Specific test file
go test ./plugins/agentready/tasks/ -run TestParseAssessmentJSON -v

# With race detection (catches concurrency bugs)
go test -race ./plugins/agentready/... -v

# All backend tests
make test
```

### Test Patterns Used

**Collector tests** — mock HTTP servers to simulate GitHub/GitLab APIs:

```go
func TestFetchGithubAssessment(t *testing.T) {
    // Create a fake HTTP server that responds like GitHub
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]interface{}{
            "content": base64.StdEncoding.EncodeToString(
                []byte(`{"score": 85}`),
            ),
        })
    }))
    defer server.Close()

    result, err := fetchGithubAssessment(server.URL, "owner/repo", "path", "token")
    assert.NoError(t, err)
    assert.Contains(t, result, `"score": 85`)
}
```

**Extractor tests** — validate JSON parsing:

```go
func TestParseAssessmentJSON(t *testing.T) {
    rawJSON := `{
        "overall_score": 85.5,
        "findings": [...]
    }`
    assessment, findings, err := parseAssessmentJSON(rawJSON, repoId)
    assert.NoError(t, err)
    assert.Equal(t, 85.5, assessment.OverallScore)
    assert.Len(t, findings, expectedCount)
}
```

**Calculator tests** — validate aggregation math:

```go
func TestCalculateMetricsFromFindings(t *testing.T) {
    findings := []AgentReadyFinding{
        {Tier: 1, Status: "pass", Score: ptr(100.0)},
        {Tier: 1, Status: "fail", Score: ptr(0.0)},
    }
    metric := calculateMetricsFromFindings(findings, "repo1", time.Now())
    assert.Equal(t, 50.0, metric.Tier1PassRate)  // 1 pass / 2 total = 50%
}
```

### Lint and Format

```bash
cd backend
make lint    # golangci-lint
make fmt     # gofmt + goimports
```

---

## 11. Running & Debugging Locally

### Start Infrastructure

```bash
# Start MySQL only (lightweight local dev)
podman compose -f docker-compose-dev.yml up -d mysql

# Or start everything (MySQL + DevLake + Grafana + Config-UI)
podman compose -f docker-compose-dev.yml up -d
```

### Build and Run

```bash
cd backend
make build       # Build all plugins + server
make dev         # Build + run

# Or build just the agentready plugin
go build -o bin/plugins/agentready ./plugins/agentready/agentready.go
```

### Verify Plugin Loaded

```bash
curl -s http://localhost:4000/api/plugins | jq '.[] | select(.plugin == "agentready")'
```

### Check Tables Created

```bash
podman compose -f docker-compose-dev.yml exec mysql \
  mysql -umerico -pmerico lake -e "SHOW TABLES LIKE '_tool_agentready%';"
```

### Test API Endpoints

```bash
# List assessments
curl -s http://localhost:8080/plugins/agentready/assessments | jq .

# Get stats
curl -s http://localhost:8080/plugins/agentready/stats | jq .

# Create scope config
curl -X POST http://localhost:8080/plugins/agentready/scope-configs \
  -H 'Content-Type: application/json' \
  -d '{"branch": "main", "assessment_file_path": ".agentready/assessment-latest.json"}'
```

### Database Debugging

```bash
# Connect to MySQL
podman compose -f docker-compose-dev.yml exec mysql mysql -umerico -pmerico lake

# Useful queries:
SELECT * FROM _tool_agentready_assessments ORDER BY assessed_at DESC LIMIT 5;
SELECT * FROM _tool_agentready_findings WHERE assessment_id = 'some-id';
SELECT * FROM _tool_agentready_metrics ORDER BY assessed_at DESC LIMIT 5;
SELECT * FROM _devlake_migration_history WHERE script_name LIKE '%agentready%';
```

### Service URLs

| Service | URL |
|---------|-----|
| Config UI | http://localhost:4000 |
| DevLake API | http://localhost:8080 |
| Grafana | http://localhost:4000/grafana/ |
| MySQL | localhost:3306 |

---

## 12. Key Files Reference

### Core Framework (read these to understand the system)

| File | What You'll Learn |
|------|------------------|
| `core/plugin/plugin_task.go` | SubTaskMeta, TaskContext, SubTaskContext interfaces |
| `core/plugin/plugin_meta.go` | PluginMeta, PluginInit interfaces |
| `core/plugin/plugin_api.go` | How API endpoints are registered |
| `core/plugin/plugin_blueprint.go` | Pipeline plan generation |
| `core/plugin/hub.go` | Plugin registry (RegisterPlugin, GetPlugin) |
| `core/dal/dal.go` | All database operations available |
| `core/models/common/base.go` | NoPKModel, Model, Scope, ScopeConfig base types |
| `core/runner/run_task.go` | How subtasks execute in sequence |
| `core/runner/loader.go` | How plugins are loaded from .so files |

### AgentReady Plugin (your code)

| File | Purpose |
|------|---------|
| `plugins/agentready/impl/impl.go` | Plugin registration, all interface implementations |
| `plugins/agentready/models/assessment.go` | Assessment table schema |
| `plugins/agentready/models/finding.go` | Finding table schema |
| `plugins/agentready/models/metric.go` | Metric table schema |
| `plugins/agentready/models/scope_config.go` | Scope config table schema |
| `plugins/agentready/tasks/task_data.go` | Options struct, RepoInfo, shared types |
| `plugins/agentready/tasks/assessment_collector.go` | Stage 1: API data collection |
| `plugins/agentready/tasks/assessment_extractor.go` | Stage 2: JSON parsing |
| `plugins/agentready/tasks/metrics_calculator.go` | Stage 3: Metric aggregation |
| `plugins/agentready/api/assessments.go` | REST API for querying data |
| `plugins/agentready/api/stats.go` | Aggregate statistics endpoint |

### Reference Plugins (study these patterns)

| Plugin | Why Read It |
|--------|------------|
| `plugins/gitextractor/` | Simple plugin with Clone → Collect pattern |
| `plugins/github/` | Full-featured data-source plugin with all interfaces |
| `plugins/refdiff/` | Metric plugin pattern (cross-plugin analysis) |
| `plugins/linker/` | Simplest metric plugin |

---

## Glossary

| Term | Definition |
|------|-----------|
| **Connection** | Credentials + endpoint for a data source (e.g., "my GitHub org token") |
| **Scope** | A unit of data within a connection (e.g., one GitHub repo) |
| **ScopeConfig** | Configuration for how to process a scope (e.g., which branch to check) |
| **Blueprint** | A saved configuration that defines what to collect and when |
| **Pipeline** | A single execution of a blueprint — contains stages and tasks |
| **Stage** | A group of tasks that run in parallel within a pipeline |
| **Task** | One plugin's work within a stage (runs subtasks sequentially) |
| **SubTask** | A single unit of work (Collect, Extract, Convert, Calculate) |
| **Domain Repo ID** | Cross-plugin identifier: `provider:ModelType:connectionId:scopeId` |
| **DAL** | Data Access Layer — DevLake's database abstraction over GORM |
| **Tool Layer** | Raw provider-specific data (prefixed `_tool_`) |
| **Domain Layer** | Normalized cross-provider data (e.g., `repos`, `commits`) |
| **Metric Plugin** | Plugin that computes derived data from other plugins' output |
| **Data Source Plugin** | Plugin that fetches data from external APIs |

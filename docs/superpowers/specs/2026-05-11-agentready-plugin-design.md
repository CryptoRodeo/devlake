# AgentReady DevLake Plugin — Design Spec

## Overview

DevLake metric plugin that collects AI readiness assessment data from repositories and makes it available for Grafana dashboards. Replaces the standalone agentready-fleet static site with integrated DevLake analytics including historical trends.

**Plugin name:** `agentready`
**Plugin type:** Metric (runs after github/gitlab plugins)
**Data source:** `.agentready/assessment-latest.json` files in connected repos

## Architecture

### Plugin Interfaces

```go
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
```

- `RunAfter()` returns `["github", "gitlab"]`
- `IsProjectMetric()` returns `true`
- `RequiredDataEntities()` returns dependency on `repos` domain table

### Directory Structure

```
backend/plugins/agentready/
├── agentready.go                    # PluginEntry
├── impl/
│   └── impl.go                      # Interface implementations
├── models/
│   ├── assessment.go                # Assessment tool model
│   ├── finding.go                   # Finding tool model
│   ├── metric.go                    # Aggregated metrics model
│   ├── scope_config.go              # Scope configuration
│   └── migrationscripts/
│       ├── init_schema.go           # v1 schema
│       └── register.go              # Migration registry
├── api/
│   ├── init.go                      # API initialization
│   ├── assessments.go               # Assessment CRUD
│   ├── stats.go                     # Aggregated stats
│   └── scope_config.go              # Config CRUD
├── tasks/
│   ├── task_data.go                 # Shared options & task data
│   ├── assessment_collector.go      # Fetch from GitHub/GitLab API
│   ├── assessment_collector_test.go
│   ├── assessment_extractor.go      # Parse raw JSON → tool models
│   ├── assessment_extractor_test.go
│   ├── finding_extractor.go         # Parse findings from assessments
│   ├── finding_extractor_test.go
│   ├── metrics_calculator.go        # Compute aggregated stats
│   └── metrics_calculator_test.go
├── e2e/
│   ├── agentready_test.go
│   ├── raw_tables/
│   │   └── _raw_agentready_assessments.csv
│   └── snapshot_tables/
│       ├── _tool_agentready_assessments.csv
│       ├── _tool_agentready_findings.csv
│       └── _tool_agentready_metrics.csv
└── grafana/
    ├── fleet-overview.json
    ├── repo-detail.json
    └── findings-analysis.json
```

## Data Model

### `_tool_agentready_assessments`

| Column | Type | Description |
|--------|------|-------------|
| id | VARCHAR(255) PK | `{repo_id}:{commit_hash}` |
| repo_id | VARCHAR(255) | Domain layer repo ID |
| repo_name | VARCHAR(255) | Human-readable name |
| connection_id | BIGINT | Source github/gitlab connection |
| provider | VARCHAR(50) | "github" or "gitlab" |
| schema_version | VARCHAR(20) | Assessment schema version |
| overall_score | FLOAT | 0-100 |
| certification_level | VARCHAR(50) | Platinum/Gold/Silver/Bronze/NeedsImprovement/None |
| attributes_assessed | INT | Count assessed |
| attributes_total | INT | Count total |
| branch | VARCHAR(255) | Branch assessed |
| commit_hash | VARCHAR(40) | Commit SHA |
| duration_seconds | FLOAT | Assessment runtime |
| assessed_at | DATETIME | Assessment timestamp |
| collected_at | DATETIME | When DevLake fetched it |
| raw_json | LONGTEXT | Full assessment JSON |

### `_tool_agentready_findings`

| Column | Type | Description |
|--------|------|-------------|
| id | VARCHAR(255) PK | `{assessment_id}:{attribute_id}` |
| assessment_id | VARCHAR(255) FK | Links to assessment |
| repo_id | VARCHAR(255) | Denormalized for query perf |
| attribute_id | VARCHAR(255) | Attribute identifier |
| attribute_name | VARCHAR(255) | Human-readable name |
| category | VARCHAR(255) | Finding category |
| tier | INT | 1=Essential, 2=Critical, 3=Important, 4=Advanced |
| status | VARCHAR(50) | pass/fail/skipped/error/not_applicable |
| score | FLOAT NULL | 0-100, null for non-pass/fail |
| measured_value | TEXT NULL | Actual measurement |
| threshold | TEXT NULL | Target threshold |
| evidence | TEXT NULL | JSON array of evidence strings |
| remediation_summary | TEXT NULL | Fix summary |
| remediation_steps | TEXT NULL | JSON array of steps |
| default_weight | FLOAT | Attribute weight in overall score |

### `_tool_agentready_metrics` (Aggregated)

| Column | Type | Description |
|--------|------|-------------|
| id | VARCHAR(255) PK | `{repo_id}:{assessed_at}` |
| repo_id | VARCHAR(255) | Domain layer repo ID |
| assessed_at | DATETIME | Assessment timestamp |
| pass_count | INT | Findings with status=pass |
| fail_count | INT | Findings with status=fail |
| skip_count | INT | Findings skipped/error/na |
| tier1_pass_rate | FLOAT | Essential tier pass % |
| tier2_pass_rate | FLOAT | Critical tier pass % |
| tier3_pass_rate | FLOAT | Important tier pass % |
| tier4_pass_rate | FLOAT | Advanced tier pass % |
| category_scores | TEXT | JSON map of category → avg score |

### `_tool_agentready_scope_configs`

| Column | Type | Description |
|--------|------|-------------|
| id | BIGINT PK AUTO | Config ID |
| branch | VARCHAR(255) | Override branch (empty = repo default) |
| assessment_file_path | VARCHAR(500) | Override path (default: `.agentready/assessment-latest.json`) |
| exclude_repos | TEXT | JSON array of repo name patterns to skip |
| created_at | DATETIME | |
| updated_at | DATETIME | |

## Subtask Pipeline

### 1. CollectAssessments

**Purpose:** Fetch raw assessment JSON from connected repos.

**Flow:**
1. Query `project_mapping` + `repos` domain tables → list of repos in project
2. For each repo, determine provider type from domain layer
3. Load connection credentials from `_tool_github_connections` or `_tool_gitlab_connections`
4. Fetch `.agentready/assessment-latest.json`:
   - **GitHub:** `GET /repos/{owner}/{repo}/contents/{path}` → base64 decode `content` field
   - **GitLab:** `GET /api/v4/projects/{id}/repository/files/{url_encoded_path}/raw?ref={branch}`
5. Store raw JSON in `_raw_agentready_assessments`

**Error handling:**
- 404 (no file): log info, skip repo, continue
- Auth failure: log error with repo context, continue
- Rate limit: respect `X-RateLimit-*` headers, back off
- Malformed response: log error, skip, continue

### 2. ExtractAssessments

**Purpose:** Parse raw JSON into assessment tool models.

**Flow:**
1. Read from `_raw_agentready_assessments`
2. Parse JSON, extract top-level fields
3. Generate composite ID: `{repo_id}:{commit_hash}`
4. Dedup: if assessment with same ID exists, skip (same commit = same assessment)
5. Insert into `_tool_agentready_assessments`

### 3. ExtractFindings

**Purpose:** Parse findings from assessments into individual rows.

**Flow:**
1. Read assessments from `_tool_agentready_assessments` (new ones only)
2. For each assessment, parse `raw_json` findings array
3. Filter out `not_applicable` findings
4. Generate composite ID: `{assessment_id}:{attribute_id}`
5. Extract remediation details (summary, steps)
6. Insert into `_tool_agentready_findings`

### 4. CalculateMetrics

**Purpose:** Pre-compute aggregated stats for dashboard performance.

**Flow:**
1. For each assessment, count pass/fail/skip findings
2. Calculate per-tier pass rates
3. Calculate per-category average scores
4. Insert into `_tool_agentready_metrics`

## Task Options

```go
type AgentReadyOptions struct {
    ProjectName    string `json:"projectName"`
    RepoId         string `json:"repoId"`
    TimeAfter      string `json:"timeAfter"`
    Branch         string `json:"branch"`
    ScopeConfigId  uint64 `json:"scopeConfigId"`
    ScopeConfig    *AgentReadyScopeConfig `json:"scopeConfig"`
}
```

**Modes:**
- `ProjectName` set → process all repos in project (fleet mode)
- `RepoId` set → process single repo

## API Endpoints

All under `/plugins/agentready/`:

| Method | Path | Description |
|--------|------|-------------|
| GET | `/assessments` | List assessments. Query params: `projectName`, `repoId`, `certification`, `page`, `pageSize` |
| GET | `/assessments/:id` | Single assessment with full detail |
| GET | `/assessments/:id/findings` | Findings for assessment. Query params: `tier`, `status`, `category` |
| GET | `/stats` | Fleet stats. Query params: `projectName`. Returns: avg score, certification distribution, tier pass rates |
| GET | `/scope-configs` | List scope configs |
| POST | `/scope-configs` | Create scope config |
| GET | `/scope-configs/:id` | Get scope config |
| PATCH | `/scope-configs/:id` | Update scope config |
| DELETE | `/scope-configs/:id` | Delete scope config |

## Grafana Dashboards

### Dashboard 1: Fleet Overview

**Variables:** `$project` (dropdown), `$date_range` (time picker)

**Panels:**
- Score heatmap: all repos colored by certification level (stat panel grid)
- Certification distribution: pie chart
- Score trend over time: line chart (avg score across fleet per collection date)
- Top 5 / Bottom 5 repos: bar gauge panels
- Total repos assessed vs skipped: stat panels

### Dashboard 2: Repository Detail

**Variables:** `$project`, `$repo` (dropdown), `$date_range`

**Panels:**
- Score history: line chart (repo score over time)
- Current certification: stat panel with color
- Findings by tier: stacked bar (pass/fail per tier)
- Category breakdown: table with score per category
- Finding details: table with status, evidence, tier, remediation summary
- Assessment metadata: stat panels (duration, attributes assessed, branch)

### Dashboard 3: Findings Analysis

**Variables:** `$project`, `$date_range`, `$tier` (multi-select), `$status` (multi-select)

**Panels:**
- Most common failures: bar chart ranked by occurrence across fleet
- Tier pass rates over time: multi-line chart
- Category heatmap: repos × categories matrix
- Remediation backlog: table sorted by weight × tier (highest impact first)
- Finding resolution rate: showing findings that went pass→fail or fail→pass between collections

## Connection to Existing DevLake Data

The plugin reads from these existing tables (no writes):

- `project_mapping` — maps project name → plugin scopes (table `_devlake_project_mapping`)
- `repos` — domain layer repos with provider info
- `_tool_github_connections` — GitHub tokens/endpoints
- `_tool_gitlab_connections` — GitLab tokens/endpoints
- `_tool_github_repos` — GitHub repo details (owner, name)
- `_tool_gitlab_projects` — GitLab project details (project ID, path)

### Connection Discovery Flow

To fetch an assessment file, the plugin needs: (1) API credentials and (2) repo identifier (owner/name or project ID).

1. Query `project_mapping` WHERE `project_name = $projectName` → get `(plugin, scope_id, connection_id)` tuples
2. Filter for `plugin IN ("github", "gitlab")`
3. For GitHub: query `_tool_github_repos` WHERE `connection_id` and `github_id` match scope → get `owner_login`, `name`
4. For GitLab: query `_tool_gitlab_projects` WHERE `connection_id` and `gitlab_id` match scope → get `path_with_namespace`
5. Load connection from `_tool_{provider}_connections` WHERE `id = connection_id` → get token, endpoint
6. Construct API URL and fetch assessment file

## Testing Strategy

### Unit Tests
- `assessment_collector_test.go`: Mock HTTP server, test GitHub/GitLab API calls, test 404 handling, test auth header construction
- `assessment_extractor_test.go`: Parse sample JSON, verify all fields extracted correctly, test malformed JSON
- `finding_extractor_test.go`: Verify finding parsing, remediation extraction, not_applicable filtering
- `metrics_calculator_test.go`: Verify aggregate calculations, tier pass rates, category scores

### E2E Tests
- Raw table CSVs with sample assessment JSON (happy path, edge cases)
- Snapshot tables with expected output
- Test pipeline: raw → assessments → findings → metrics
- Edge cases: empty findings, all tiers, all certification levels, missing optional fields

### Test Fixtures
- Source: real assessment JSON from `~/projects/agentready/examples/`
- Sanitize: remove any sensitive paths/data
- Include: Platinum, Gold, Silver, Bronze, NeedsImprovement examples

## Error Handling Summary

| Scenario | Behavior |
|----------|----------|
| Repo has no assessment file (404) | Log info, skip, continue |
| Malformed assessment JSON | Log error with repo name, skip, continue |
| Connection auth failure | Log error, skip repo, continue |
| GitHub/GitLab rate limit | Back off per rate limit headers |
| Assessment schema version mismatch | Log warning, attempt best-effort parse |
| Duplicate assessment (same commit) | Skip silently (idempotent) |
| Empty findings array | Store assessment with 0 findings |
| Connection credentials missing | Log error, skip all repos for that connection |

## Out of Scope (YAGNI)

- Custom domain layer tables — tool tables queried directly by Grafana
- Local filesystem fetching — only GitHub/GitLab API (repos must be connected in DevLake)
- Assessment triggering — plugin only reads, does not run assessments
- Multi-file assessment history — only fetches `assessment-latest.json` per collection run
- Real-time/streaming collection — batch only, triggered by pipeline

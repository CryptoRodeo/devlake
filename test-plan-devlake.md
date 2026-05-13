# AgentReady DevLake Plugin — Local Testing Guide & Manual Test Plan

## How to Test Locally

### 1. Start MySQL (and optionally Grafana)

```bash
cd /home/bramos/projects/devlake

# MySQL only (enough for API testing)
podman compose -f docker-compose-dev.yml up -d mysql

# MySQL + Grafana (needed for dashboard testing in Phase 6)
podman compose -f docker-compose-dev.yml up -d mysql grafana
```

> **Note:** Grafana is available at `http://localhost:3002/grafana/` (trailing slash required). Do NOT use `http://localhost:3002` bare — it redirects to `localhost:4000/grafana/` which requires config-ui. The config-ui (React frontend) is **not needed** for testing the agentready plugin — all interaction is via API + Grafana dashboards.

### 2. Build the plugin

```bash
cd backend
DEVLAKE_PLUGINS=agentready make build-plugin
```

Outputs `bin/plugins/agentready/agentready.so`.

### 3. Build + run server

```bash
export ENCRYPTION_SECRET="SMOZVSJZAXOADJDZTMTWLEOJVSIHPWFMNONSWZWVIHDDMLTYLAXXXTRRVDDSMICPOZTCSREVORSVYQBFGYNIAPXVHIPIVLNLEKAIFWVWMLNMFOXESQDRGHRYYJRKJBQT"
export DB_URL="mysql://merico:merico@localhost:3306/lake?charset=utf8mb4&parseTime=True"
DEVLAKE_PLUGINS=agentready,github make godev
```

Or build everything: `make build && make run`

### 4. Trigger migration

```bash
curl -s http://localhost:8080/proceed-db-migration
```

### 5. Verify plugin loaded

```bash
curl -s http://localhost:8080/plugins | jq '.[] | select(.plugin == "agentready")'
```

### 6. Verify tables created

```bash
podman compose -f docker-compose-dev.yml exec mysql \
  mysql -umerico -pmerico lake -e "SHOW TABLES LIKE '_tool_agentready%';"
```

---

## Manual Test Plan

### Phase 1: Plugin Registration

| # | Test | How | Expected |
|---|------|-----|----------|
| 1 | Plugin loads | `curl localhost:8080/plugins \| jq` | `agentready` in list |
| 2 | Tables exist | `SHOW TABLES LIKE '_tool_agentready%'` | 4 tables: assessments, findings, metrics, scope_configs |
| 3 | Migration recorded | `SELECT * FROM _devlake_migration_history WHERE script_name LIKE '%agentready%'` | One row, version `20260511000001` |

### Phase 2: API Endpoints (no data yet)

| # | Test | How | Expected |
|---|------|-----|----------|
| 4 | List assessments | `curl localhost:8080/plugins/agentready/assessments` | `{"assessments":[], "total":0, ...}` |
| 5 | Get stats | `curl localhost:8080/plugins/agentready/stats` | `{"totalRepos":0, "averageScore":0, ...}` |
| 6 | Create scope config | `curl -X POST localhost:8080/plugins/agentready/scope-configs -H 'Content-Type: application/json' -d '{}'` | Returns config with default `assessmentFilePath` |
| 7 | List scope configs | `curl localhost:8080/plugins/agentready/scope-configs` | Array with config from #6 |
| 8 | Delete scope config | `curl -X DELETE localhost:8080/plugins/agentready/scope-configs/1` | 204 |

### Phase 3: Pipeline with Real Repos

Requires a GitHub/GitLab connection already configured in DevLake with repos that have `.agentready/assessment-latest.json`.

| # | Test | How | Expected |
|---|------|-----|----------|
| 9 | Run pipeline | `curl -X POST localhost:8080/pipelines -H 'Content-Type: application/json' -d '{"name":"test-agentready","plan":[[{"plugin":"agentready","options":{"projectName":"YOUR_PROJECT"}}]]}'` | Pipeline created, returns ID |
| 10 | Check pipeline status | `curl localhost:8080/pipelines/{id}` | Status eventually `TASK_COMPLETED` |
| 11 | Assessments populated | `curl localhost:8080/plugins/agentready/assessments` | Non-empty list with scores |
| 12 | Findings populated | `curl localhost:8080/plugins/agentready/assessments/{id}/findings` | Array of findings |
| 13 | Stats populated | `curl localhost:8080/plugins/agentready/stats` | `totalRepos > 0`, `averageScore > 0` |

### Phase 4: Pipeline Without Real Repos (Seed Data)

If no repos have assessment files, manually insert test data:

```sql
INSERT INTO _tool_agentready_assessments
  (id, repo_id, repo_name, provider, overall_score, certification_level,
   attributes_assessed, attributes_total, branch, commit_hash, assessed_at, collected_at, raw_json)
VALUES
  ('test:abc123', 'github:GithubRepo:1:999', 'test-org/test-repo', 'github',
   85.5, 'Gold', 20, 25, 'main', 'abc123', NOW(), NOW(),
   '{"schema_version":"1.0.0","repository":{"name":"test-repo","branch":"main","commit_hash":"abc123"},"timestamp":"2026-05-11T12:00:00Z","overall_score":85.5,"certification_level":"Gold","attributes_assessed":20,"attributes_total":25,"duration_seconds":5.2,"findings":[{"attribute":{"id":"doc-readme","name":"README","category":"Docs","tier":1,"default_weight":0.8},"status":"pass","score":100.0,"evidence":["ok"]},{"attribute":{"id":"sec-secrets","name":"No Secrets","category":"Security","tier":2,"default_weight":0.9},"status":"fail","score":30.0,"remediation":{"summary":"Remove secrets","steps":["step1"]}}]}');
```

Then verify APIs return data, and check Grafana dashboards at `localhost:3002/grafana/`. See Phase 6 for dashboard import instructions.

### Phase 5: Error Handling

| # | Test | How | Expected |
|---|------|-----|----------|
| 14 | Invalid repo ID | Pipeline with `repoId: "bad-format"` | Logs warning, no crash |
| 15 | Missing both options | Pipeline with empty options | Error: "either repoId or projectName is required" |
| 16 | 404 assessment file | Pipeline with repo that has no `.agentready/` file | Logs info "No assessment file found", skips repo |
| 17 | Idempotent re-run | Run same pipeline twice | Same data, no duplicates (composite PK dedup) |

### Phase 5b: Testing with Actual Repositories

This section walks you through testing the AgentReady plugin against **real repositories** that have (or will have) a `.agentready/assessment-latest.json` file. It covers how DevLake connects to GitHub/GitLab, how to add repos, and how to run the pipeline.

#### Background: How DevLake Connects to Repos

DevLake doesn't clone repos directly. Instead:

1. You create a **Connection** — tells DevLake how to talk to GitHub/GitLab (API endpoint + access token).
2. You add **Scopes** (repos) to that connection — tells DevLake which repos to track.
3. You create a **Project** and assign scopes to it — groups repos for pipeline runs.
4. The AgentReady plugin runs a **Pipeline** that fetches `.agentready/assessment-latest.json` from each repo via the GitHub/GitLab API.

#### Target Repositories

From `~/projects/agentready-fleet/repos.yaml`:

| Name | Provider | Repo | Branch |
|------|----------|------|--------|
| rhtas-console-ui | GitHub | securesign/rhtas-console-ui | main |
| trustify-ui | GitHub | guacsec/trustify-ui | main |
| tsd-ui | GitHub | tsd-ui/tsd-ui | main |
| agentready | GitHub | cryptorodeo/agentready | demo |
| ui-packages.redhat.com | GitLab (CEE) | hosted-pulp/ui-packages.redhat.com | agentready-check |

> **Note:** The GitLab repo uses `gitlab.cee.redhat.com` (internal). You'll need a separate GitLab connection with custom endpoint and SSL settings. Start with the GitHub repos — they're simpler.

#### Prerequisites

- DevLake running locally (server + MySQL — see "How to Test Locally" above)
- A **GitHub Personal Access Token** with `repo` scope (for reading file contents)
  - Create one at: https://github.com/settings/tokens → "Generate new token (classic)" → check `repo` → Generate
- (Optional) A **GitLab Personal Access Token** for `gitlab.cee.redhat.com` with `read_api` scope
- The repos must contain `.agentready/assessment-latest.json` on their target branch

#### Step 1: Verify DevLake is Running

```bash
# Check server responds
curl -s http://localhost:8080/ping

# Check plugin is loaded
curl -s http://localhost:8080/plugins | jq '.[].plugin' | grep agentready
```

Both should succeed before continuing.

#### Step 2: Create a GitHub Connection

A "connection" tells DevLake how to authenticate with GitHub's API.

```bash
export GITHUB_TOKEN="YOUR_GITHUB_TOKEN_HERE"

curl -s -X POST http://localhost:8080/plugins/github/connections \
  -H 'Content-Type: application/json' \
  -d "{
    \"name\": \"GitHub - AgentReady Test\",
    \"endpoint\": \"https://api.github.com/\",
    \"authMethod\": \"AccessToken\",
    \"token\": \"$GITHUB_TOKEN\",
    \"rateLimitPerHour\": 4500
  }" | jq .
```

> **Important:** `authMethod` is required — valid values: `AccessToken`, `BasicAuth`, `AppKey`. The command uses double quotes so `$GITHUB_TOKEN` expands (single quotes would send it as a literal string).

**Save the `id` from the response** — you'll need it (e.g., `"id": 1`).

To verify:

```bash
curl -s http://localhost:8080/plugins/github/connections | jq .
```

#### Step 3: Add Repos (Scopes) to the Connection

Each repo is a "scope" attached to a connection. Replace `CONNECTION_ID` with the ID from Step 2.

> **Important:** The request body must be wrapped in `{"data": [...]}` (not a bare array). Each scope requires `githubId` — the numeric repo ID that GitHub assigns. Find it via: `curl -s -H "Authorization: token $GITHUB_TOKEN" https://api.github.com/repos/OWNER/REPO | jq .id`

```bash
# First, look up the numeric GitHub IDs for each repo
for repo in cryptorodeo/agentready cryptorodeo/agentready-fleet guacsec/trustify-ui tsd-ui/tsd-ui; do
  echo "$repo: $(curl -s -H "Authorization: token $GITHUB_TOKEN" https://api.github.com/repos/$repo | jq .id)"
done

# Add all three repos at once (replace GITHUB_ID_* with actual IDs from above)
curl -s -X PUT "http://localhost:8080/plugins/github/connections/1/scopes" \
  -H 'Content-Type: application/json' \
  -d '{
    "data": [
      {
        "githubId": 994879996,
        "fullName": "securesign/rhtas-console-ui",
        "name": "rhtas-console-ui"
      },
      {
        "githubId": 770424376,
        "fullName": "guacsec/trustify-ui",
        "name": "trustify-ui"
      },
      {
        "githubId": 1172483136,
        "fullName": "tsd-ui/tsd-ui",
        "name": "tsd-ui"
      },
      {
        "githubId": 1233448046,
        "fullName": "cryptorodeo/agentready-fleet",
        "name": "agentready-fleet"
      },
      {
        "githubId": 1224854195,
        "fullName": "cryptorodeo/agentready",
        "name": "agentready"
      }
    ]
  }' | jq .
```

Verify scopes were added:

```bash
curl -s "http://localhost:8080/plugins/github/connections/1/scopes" | jq '.scopes[].scope.fullName'
```

Should show all four repo names.

#### Step 4: Create a DevLake Project

A "project" groups repos so the AgentReady pipeline can process them in one run.

```bash
curl -s -X POST http://localhost:8080/projects \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "TSD-UI AgentReady Fleet",
    "description": "Testing AgentReady with real repos"
  }' | jq .
```

#### Step 5: Assign Repos to the Project

The AgentReady plugin discovers repos via the `project_mapping` table. Insert mappings directly using SQL.

The repo ID format is `github:GithubRepo:CONNECTION_ID:GITHUB_ID`. Replace `CONNECTION_ID` with Step 2's ID and `GITHUB_ID` with each repo's numeric `githubId` from Step 3.

```bash
podman compose -f docker-compose-dev.yml exec mysql \
  mysql -umerico -pmerico lake -e "
    INSERT INTO project_mapping (project_name, \`table\`, row_id) VALUES
    ('TSD-UI AgentReady Fleet', 'repos', 'github:GithubRepo:1:994879996'),
    ('TSD-UI AgentReady Fleet', 'repos', 'github:GithubRepo:1:770424376'),
    ('TSD-UI AgentReady Fleet', 'repos', 'github:GithubRepo:1:1172483136'),
    ('TSD-UI AgentReady Fleet', 'repos', 'github:GithubRepo:1:1233448046'),
    ('TSD-UI AgentReady Fleet', 'repos', 'github:GithubRepo:1:1224854195');
  "
```

Verify mappings were created:

```bash
podman compose -f docker-compose-dev.yml exec mysql \
  mysql -umerico -pmerico lake -e \
  "SELECT project_name, row_id FROM project_mapping WHERE project_name = 'TSD-UI AgentReady Fleet';"
```

> **Why SQL instead of the API?** The blueprint PATCH endpoint (`PATCH /blueprints/:id`) validates all plugins referenced in the connection — including `github_graphql`, which isn't loaded when building only the `agentready` and `github` plugins. Direct SQL insert bypasses this validation and is sufficient for agentready testing.

#### Step 6: Run the AgentReady Pipeline

**Option A — By project name** (processes all repos in the project):

```bash
curl -s -X POST http://localhost:8080/pipelines \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "agentready-fleet-test",
    "plan": [[{
      "plugin": "agentready",
      "options": {
        "projectName": "TSD-UI AgentReady Fleet"
      }
    }]]
  }' | jq .
```

**Option B — By single repo ID** (test one repo at a time):

```bash
curl -s -X POST http://localhost:8080/pipelines \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "agentready-single-test",
    "plan": [[{
      "plugin": "agentready",
      "options": {
        "repoId": "github:GithubRepo:CONNECTION_ID:GITHUB_REPO_ID"
      }
    }]]
  }' | jq .
```

**Option C — With specific branch** (for repos where the assessment file is not on the default branch):

```bash
# Example: cryptorodeo/agentready has assessment on 'demo' branch
curl -s -X POST http://localhost:8080/pipelines \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "agentready-fleet-test",
    "plan": [[{
      "plugin": "agentready",
      "options": {
        "repoId": "github:GithubRepo:1:1224854195",
        "branch": "demo"
      }
    }]]
  }' | jq .
```

> **Note:** The `branch` option works with both `repoId` and `projectName`. When using `projectName`, all repos in the project will be fetched from the specified branch. Per-repo branch overrides require separate pipeline runs.

Save the pipeline `id` from the response.

#### Step 7: Monitor Pipeline

```bash
# Check status (repeat until "status": "TASK_COMPLETED" or "TASK_FAILED")
curl -s http://localhost:8080/pipelines/1 | jq '{status, message, finishedAt}'
```

If it fails, check logs:

```bash
# Server logs (if running via `make dev`)
# Look for lines with "agentready" in the terminal output

# Or if running in container:
podman compose -f docker-compose-dev.yml logs -f devlake | grep -i agentready
```

#### Step 8: Verify Results

```bash
# Check assessments were collected
curl -s http://localhost:8080/plugins/agentready/assessments | jq .

# Check stats
curl -s http://localhost:8080/plugins/agentready/stats | jq .

# Check findings for a specific assessment
ASSESSMENT_ID=$(curl -s http://localhost:8080/plugins/agentready/assessments | jq -r '.assessments[0].id')
curl -s "http://localhost:8080/plugins/agentready/assessments/$ASSESSMENT_ID/findings" | jq .
```

#### Step Verification Checklist

| # | Test | How | Expected |
|---|------|-----|----------|
| A | GitHub connection created | `curl localhost:8080/plugins/github/connections` | Connection with your token |
| B | Scopes added | `curl localhost:8080/plugins/github/connections/ID/scopes \| jq '.scopes[].scope.fullName'` | 4 repos listed |
| C | Project exists | `curl localhost:8080/projects` | "TSD-UI AgentReady Fleet" |
| D | Pipeline completes | `curl localhost:8080/pipelines/ID` | `"status": "TASK_COMPLETED"` |
| E | Assessments populated | `curl localhost:8080/plugins/agentready/assessments` | Non-empty list with scores |
| F | Stats non-zero | `curl localhost:8080/plugins/agentready/stats` | `totalRepos > 0` |

#### Troubleshooting

| Problem | Cause | Fix |
|---------|-------|-----|
| `Field validation for 'AuthMethod' failed` | Missing `authMethod` in connection request | Add `"authMethod": "AccessToken"` to request body |
| `cannot unmarshal array into Go value of type map` | Bare array `[{...}]` sent to PUT scopes | Wrap in `{"data": [{...}]}` |
| `invalid character ']' looking for beginning of value` | Trailing comma in JSON array | Remove comma after last item before `]` — JSON doesn't allow trailing commas |
| `Plugin github_graphql doesn't exist` | Blueprint PATCH validates all GitHub sub-plugins | Use direct SQL insert into `project_mapping` instead (see Step 5) |
| Pipeline completes but 0 assessments (project mode) | `project_mapping` table empty | Run Step 5 SQL insert, verify with `SELECT * FROM project_mapping` |
| Pipeline completes but 0 assessments (repo mode) | Repo doesn't have `.agentready/assessment-latest.json` | Verify file exists: `curl -H "Authorization: token $GITHUB_TOKEN" https://api.github.com/repos/OWNER/REPO/contents/.agentready/assessment-latest.json` |
| `401 Unauthorized` in logs | Bad/expired GitHub token | Regenerate token, update connection |
| `404 Not Found` for repo | Wrong `fullName` or repo is private without token scope | Verify repo name, check token has `repo` scope |
| `connection not found` | Wrong CONNECTION_ID in repoId | Check `curl localhost:8080/plugins/github/connections` for correct ID |
| Pipeline stuck `TASK_RUNNING` | Rate limiting or network issue | Check server logs for errors |
| Grafana `localhost:3002` shows "site can't be reached" | Grafana redirects to `localhost:4000/grafana/` | Use `http://localhost:3002/grafana/` instead (trailing slash required) |
| `$GITHUB_TOKEN` sent as literal string | Single-quoted shell string prevents variable expansion | Use double quotes for outer string with escaped inner quotes (see Step 2) |

#### (Optional) Adding the GitLab CEE Repo

For `gitlab.cee.redhat.com` repos, create a separate GitLab connection:

```bash
curl -s -X POST http://localhost:8080/plugins/gitlab/connections \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "GitLab CEE - AgentReady Test",
    "endpoint": "https://gitlab.cee.redhat.com/api/v4/",
    "token": "YOUR_GITLAB_CEE_TOKEN_HERE",
    "rateLimitPerHour": 3600
  }' | jq .
```

> **Note:** If you get SSL errors, you may need to configure the custom Red Hat CA certificate. See the "Custom CA (CEE GitLab)" section in `CLAUDE.md`.

Then add scopes and run pipelines the same way, but using `gitlab` plugin and `gitlab:GitlabProject:CONNECTION_ID:PROJECT_ID` format for repo IDs.

### Phase 6: Grafana Dashboards

Dashboard JSON files are in `backend/plugins/agentready/grafana/`:

- `fleet-overview.json`
- `repo-detail.json`
- `findings-analysis.json`

> **Grafana must be running** — see Step 1 above.
>
> **Access URL:** `http://localhost:3002/grafana/` (trailing slash required). Do NOT use `http://localhost:3002` — it redirects to `localhost:4000/grafana/` which won't work unless config-ui is running.
>
> **Login:** Anonymous access is enabled by default. If prompted, use `admin` / `admin`.
>
> **Dashboards are NOT auto-provisioned.** Import them manually:
> 1. Go to `http://localhost:3002/grafana/dashboard/import`
> 2. Click "Upload dashboard JSON file"
> 3. Select dashboard file, choose the **mysql** datasource when prompted, click Import
> 4. Repeat for each dashboard

| # | Test | How | Expected |
|---|------|-----|----------|
| 18 | Import fleet dashboard | Import → upload `backend/plugins/agentready/grafana/fleet-overview.json` | Dashboard loads, panels show data |
| 19 | Import repo detail | Import → upload `backend/plugins/agentready/grafana/repo-detail.json` | Repo dropdown populated, panels work |
| 20 | Import findings analysis | Import → upload `backend/plugins/agentready/grafana/findings-analysis.json` | Failure rankings, tier pass rates render |

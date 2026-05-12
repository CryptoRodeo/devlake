# AgentReady DevLake Plugin — Local Testing Guide & Manual Test Plan

## How to Test Locally

### 1. Start MySQL

```bash
cd /home/bramos/projects/devlake
podman compose -f docker-compose-dev.yml up -d mysql
```

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
make dev
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

Then verify APIs return data, and check Grafana dashboards at `localhost:4000/grafana/`.

### Phase 5: Error Handling

| # | Test | How | Expected |
|---|------|-----|----------|
| 14 | Invalid repo ID | Pipeline with `repoId: "bad-format"` | Logs warning, no crash |
| 15 | Missing both options | Pipeline with empty options | Error: "either repoId or projectName is required" |
| 16 | 404 assessment file | Pipeline with repo that has no `.agentready/` file | Logs info "No assessment file found", skips repo |
| 17 | Idempotent re-run | Run same pipeline twice | Same data, no duplicates (composite PK dedup) |

### Phase 6: Grafana Dashboards

| # | Test | How | Expected |
|---|------|-----|----------|
| 18 | Import fleet dashboard | Grafana → Import → paste `fleet-overview.json` | Dashboard loads, panels show data |
| 19 | Import repo detail | Same with `repo-detail.json` | Repo dropdown populated, panels work |
| 20 | Import findings analysis | Same with `findings-analysis.json` | Failure rankings, tier pass rates render |

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
	"github.com/apache/incubator-devlake/core/log"
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

func discoverRepos(db dal.Dal, options *AgentReadyOptions, logger log.Logger) ([]*RepoInfo, errors.Error) {
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

	cleaned := strings.ReplaceAll(result.Content, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", fmt.Errorf("decoding base64 content: %w", err)
	}

	return string(decoded), nil
}

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

	const maxAssessmentSize = 10 << 20 // 10 MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAssessmentSize))
	if err != nil {
		return "", fmt.Errorf("reading GitLab response: %w", err)
	}

	return string(body), nil
}

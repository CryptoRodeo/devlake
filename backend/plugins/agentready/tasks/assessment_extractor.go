package tasks

import (
	"encoding/json"
	"fmt"
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
	SchemaVersion      string         `json:"schema_version"`
	Repository         repositoryJSON `json:"repository"`
	Timestamp          string         `json:"timestamp"`
	OverallScore       float64        `json:"overall_score"`
	CertificationLevel string         `json:"certification_level"`
	AttributesAssessed int            `json:"attributes_assessed"`
	AttributesTotal    int            `json:"attributes_total"`
	DurationSeconds    float64        `json:"duration_seconds"`
	Findings           []findingJSON  `json:"findings"`
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

	var rawAssessments []models.AgentReadyAssessment
	err := db.All(&rawAssessments,
		dal.From(&models.AgentReadyAssessment{}),
		dal.Where("raw_json != ''"),
	)
	if err != nil {
		return errors.Default.Wrap(err, "failed to query raw assessments")
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

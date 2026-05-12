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

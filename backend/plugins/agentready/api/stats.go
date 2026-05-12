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
			"totalRepos":                len(assessments),
			"averageScore":              avgScore,
			"certificationDistribution": certDist,
		},
		Status: http.StatusOK,
	}, nil
}

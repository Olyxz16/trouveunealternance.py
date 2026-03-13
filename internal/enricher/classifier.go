package enricher

import (
	"context"
	"fmt"
	"jobhunter/internal/db"
	"jobhunter/internal/llm"
	"strings"
)

type CompanyScore struct {
	RelevanceScore      int      `json:"relevance_score"`
	CompanyType         string   `json:"company_type"` // TECH, TECH_ADJACENT, NON_TECH
	HasInternalTechTeam bool     `json:"has_internal_tech_team"`
	TechTeamSignals     []string `json:"tech_team_signals"`
	Reasoning           string   `json:"reasoning"`
}

const ScoreSystemPrompt = `You are evaluating French companies as potential internship hosts for a DevOps/backend student.

Classification:
- TECH: Product is software/infra.
- TECH_ADJACENT: Non-tech business (retail, bank, logistics) but large enough (100+ emp) to have internal IT/infra.
- NON_TECH: No meaningful tech needs.

For TECH_ADJACENT, look for signals: digital transformation, tech blog, job postings for devs despite non-tech core business.
Score 0-10 based on stack relevance and company profile.
`

type Classifier struct {
	llm *llm.Client
	db  *db.DB
}

func NewClassifier(llmClient *llm.Client, database *db.DB) *Classifier {
	return &Classifier{
		llm: llmClient,
		db:  database,
	}
}

func (c *Classifier) ScoreCompany(ctx context.Context, comp db.Company, runID string) (CompanyScore, error) {
	prompt := fmt.Sprintf(`Company: %s
NAF: %s - %s
City: %s
Size: %s employees
Description: %s`,
		comp.Name,
		comp.NAFCode.String,
		comp.NAFLabel.String,
		comp.City.String,
		comp.HeadcountRange.String,
		comp.Description.String,
	)

	var score CompanyScore
	req := llm.CompletionRequest{
		System: ScoreSystemPrompt,
		User:   prompt,
	}

	err := c.llm.CompleteJSON(ctx, req, "score_company", runID, &score)
	if err != nil {
		return CompanyScore{}, err
	}

	// Apply caps and defaults from PLAN.md
	if score.CompanyType == "TECH_ADJACENT" && score.RelevanceScore > 7 {
		score.RelevanceScore = 7
	}

	// Update DB
	updates := map[string]interface{}{
		"relevance_score":         score.RelevanceScore,
		"company_type":            score.CompanyType,
		"has_internal_tech_team":  score.HasInternalTechTeam,
		"tech_team_signals":      strings.Join(score.TechTeamSignals, ", "),
		"notes":                   fmt.Sprintf("%s | %s", comp.Notes.String, score.Reasoning),
		"status":                  "NEW",
	}

	if score.CompanyType == "NON_TECH" {
		updates["status"] = "NOT_TECH"
	}

	err = c.db.UpdateCompany(comp.ID, updates)
	if err != nil {
		return score, fmt.Errorf("failed to update company in DB: %w", err)
	}

	return score, nil
}

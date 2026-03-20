package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Company struct {
	ID                  int            `json:"id"`
	Name                string         `json:"name"`
	Siren               sql.NullString `json:"siren"`
	NAFCode             sql.NullString `json:"naf_code"`
	NAFLabel            sql.NullString `json:"naf_label"`
	City                sql.NullString `json:"city"`
	Department          sql.NullString `json:"department"`
	HeadcountRange      sql.NullString `json:"headcount_range"`
	Website             sql.NullString `json:"website"`
	LinkedinURL         sql.NullString `json:"linkedin_url"`
	CareersPageURL      sql.NullString `json:"careers_page_url"`
	TechStack           sql.NullString `json:"tech_stack"`
	Status              string         `json:"status"`
	RelevanceScore      int            `json:"relevance_score"`
	Notes               sql.NullString `json:"notes"`
	DateFound           string         `json:"date_found"`
	UpdatedAt           string         `json:"updated_at"`
	CompanyType         string         `json:"company_type"`
	HasInternalTechTeam sql.NullBool   `json:"has_internal_tech_team"`
	TechTeamSignals     sql.NullString `json:"tech_team_signals"`
	PrimaryContactID    sql.NullInt64  `json:"primary_contact_id"`
	CompanyEmail        sql.NullString `json:"company_email"`
}

func (c Company) MarshalJSON() ([]byte, error) {
	type Alias Company
	return json.Marshal(&struct {
		Alias
		Siren               string `json:"siren"`
		NAFCode             string `json:"naf_code"`
		NAFLabel            string `json:"naf_label"`
		City                string `json:"city"`
		Department          string `json:"department"`
		HeadcountRange      string `json:"headcount_range"`
		Website             string `json:"website"`
		LinkedinURL         string `json:"linkedin_url"`
		CareersPageURL      string `json:"careers_page_url"`
		TechStack           string `json:"tech_stack"`
		Notes               string `json:"notes"`
		HasInternalTechTeam *bool  `json:"has_internal_tech_team"`
		TechTeamSignals     string `json:"tech_team_signals"`
		PrimaryContactID    int64  `json:"primary_contact_id"`
		CompanyEmail        string `json:"company_email"`
	}{
		Alias:               Alias(c),
		Siren:               c.Siren.String,
		NAFCode:             c.NAFCode.String,
		NAFLabel:            c.NAFLabel.String,
		City:                c.City.String,
		Department:          c.Department.String,
		HeadcountRange:      c.HeadcountRange.String,
		Website:             c.Website.String,
		LinkedinURL:         c.LinkedinURL.String,
		CareersPageURL:      c.CareersPageURL.String,
		TechStack:           c.TechStack.String,
		Notes:               c.Notes.String,
		HasInternalTechTeam: func() *bool {
			if c.HasInternalTechTeam.Valid {
				return &c.HasInternalTechTeam.Bool
			}
			return nil
		}(),
		TechTeamSignals:     c.TechTeamSignals.String,
		PrimaryContactID:    c.PrimaryContactID.Int64,
		CompanyEmail:        c.CompanyEmail.String,
	})
}

func (db *DB) UpsertCompany(c *Company) (int, bool, error) {
	if c.DateFound == "" {
		c.DateFound = time.Now().Format("2006-01-02")
	}
	if c.Status == "" {
		c.Status = "NEW"
	}

	var id int
	var err error
	if c.Siren.Valid && c.Siren.String != "" {
		err = db.QueryRow("SELECT id FROM companies WHERE siren=?", c.Siren.String).Scan(&id)
	} else {
		err = db.QueryRow("SELECT id FROM companies WHERE name=? AND city=?", c.Name, c.City.String).Scan(&id)
	}

	if err == nil {
		return id, false, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}

	res, err := db.Exec(`
		INSERT INTO companies (
			name, siren, naf_code, naf_label, city, department,
			headcount_range, website, linkedin_url, tech_stack,
			careers_page_url, status, relevance_score, notes, date_found,
			company_type, has_internal_tech_team, tech_team_signals, company_email
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		c.Name, c.Siren, c.NAFCode, c.NAFLabel, c.City, c.Department,
		c.HeadcountRange, c.Website, c.LinkedinURL, c.TechStack,
		c.CareersPageURL, c.Status, c.RelevanceScore, c.Notes, c.DateFound,
		c.CompanyType, c.HasInternalTechTeam, c.TechTeamSignals, c.CompanyEmail,
	)
	if err != nil {
		return 0, false, err
	}

	lastID, err := res.LastInsertId()
	if err != nil {
		return 0, false, err
	}

	return int(lastID), true, nil
}

func (db *DB) UpdateCompany(id int, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}

	fields["updated_at"] = time.Now().Format(time.RFC3339)

	var setClauses []string
	var args []interface{}
	for k, v := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s=?", k))
		args = append(args, v)
	}
	args = append(args, id)

	query := fmt.Sprintf("UPDATE companies SET %s WHERE id=?", strings.Join(setClauses, ", "))
	_, err := db.Exec(query, args...)
	return err
}

const allCompanyCols = `id, name, siren, naf_code, naf_label, city, department, headcount_range, website, linkedin_url, careers_page_url, tech_stack, status, relevance_score, notes, date_found, updated_at, primary_contact_id, company_type, has_internal_tech_team, tech_team_signals, company_email`

func (db *DB) GetCompany(id int) (*Company, error) {
	var c Company
	query := fmt.Sprintf("SELECT %s FROM companies WHERE id=?", allCompanyCols)
	err := db.QueryRow(query, id).Scan(
		&c.ID, &c.Name, &c.Siren, &c.NAFCode, &c.NAFLabel,
		&c.City, &c.Department,
		&c.HeadcountRange,
		&c.Website, &c.LinkedinURL,
		&c.CareersPageURL,
		&c.TechStack,
		&c.Status, &c.RelevanceScore, &c.Notes,
		&c.DateFound, &c.UpdatedAt, &c.PrimaryContactID, &c.CompanyType, &c.HasInternalTechTeam, &c.TechTeamSignals,
		&c.CompanyEmail,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (db *DB) GetCompaniesForEnrichment() ([]Company, error) {
	query := fmt.Sprintf("SELECT %s FROM companies WHERE status = 'NEW' AND (primary_contact_id IS NULL OR company_type = 'UNKNOWN')", allCompanyCols)
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var companies []Company
	for rows.Next() {
		var c Company
		err := rows.Scan(
			&c.ID, &c.Name, &c.Siren, &c.NAFCode, &c.NAFLabel,
			&c.City, &c.Department,
			&c.HeadcountRange,
			&c.Website, &c.LinkedinURL,
			&c.CareersPageURL,
			&c.TechStack,
			&c.Status, &c.RelevanceScore, &c.Notes,
			&c.DateFound, &c.UpdatedAt, &c.PrimaryContactID, &c.CompanyType, &c.HasInternalTechTeam, &c.TechTeamSignals,
			&c.CompanyEmail,
		)
		if err != nil {
			return nil, err
		}
		companies = append(companies, c)
	}
	return companies, nil
}

type Job struct {
	ID             int    `json:"id"`
	Company        string `json:"company"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	DateFound      string `json:"date_found"`
	RelevanceScore int    `json:"relevance_score"`
	Type           string `json:"type"`
}

func (db *DB) GetJobs(limit int) ([]Job, error) {
	rows, err := db.Query(`
        SELECT id, company, title, status, date_found, relevance_score, type
        FROM jobs ORDER BY relevance_score DESC, date_found DESC LIMIT ?
    `, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Company, &j.Title, &j.Status,
			&j.DateFound, &j.RelevanceScore, &j.Type); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

package enricher

import (
	"context"
	"encoding/json"
	"fmt"
	"jobhunter/internal/llm"
	"jobhunter/internal/scraper"
	"strings"
)

// PeoplePageData — extracted from LinkedIn People tab.
// Individual people ONLY.
type PeoplePageData struct {
	Contacts []IndividualContact `json:"contacts"`
}

type IndividualContact struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	LinkedinURL string `json:"linkedin_url"` // MUST be /in/ personal profile, never /company/
	Email       string `json:"email"`        // personal work email if publicly visible; empty if not
	Confidence  string `json:"confidence"`   // e.g. 'probable', 'hallucinated'
}

// IndividualProfileData — extracted from a personal LinkedIn /in/ profile.
// Used to enrich a contact found on the People tab.
type IndividualProfileData struct {
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Email       string   `json:"email"`        // if visible on profile
	RecentPosts []string `json:"recent_posts"` // topics/themes of recent activity
	Background  string   `json:"background"`   // 1-2 sentence summary of their profile
	TechStack   []string `json:"tech_stack"`   // technologies mentioned on their profile
}

const PeopleExtractionPrompt = `You are extracting a list of individual employees from a LinkedIn People tab or from search engine results.

Return ALL relevant contacts found (up to 5 people). Do not return just one.

CRITICAL:
- ONLY return people explicitly mentioned in the provided content.
- NEVER hallucinate or invent names (e.g., do NOT return "John Doe", "Jane Smith", "Mike Smith" if they are not in the text).
- If no real people are found, return an empty list.

STRICT RULES:
- linkedin_url MUST be a full, absolute personal LinkedIn profile URL starting with https://www.linkedin.com/in/ — never a relative path like /in/... — NEVER a /company/ URL
- email: only include if a personal work email is explicitly visible on the page — do NOT include generic company emails (contact@, info@, careers@, jobs@) — leave empty if unsure
- name and role are required — skip entries where you cannot determine both
- Focus on: CTO, VP Engineering, Engineering Manager, Tech Lead, DevOps Engineer, Infrastructure Manager, IT Director, Technical Recruiter

Return a JSON object with a single field "contacts" containing the list.`

const ContactRankingPrompt = `Given this list of contacts at a %s company, pick the single BEST person to cold-email for a DevOps/backend internship.

Priority order: 
1. Technical Leadership: CTO, VP Engineering, Engineering Manager, Tech Lead, Infrastructure Manager, IT Director.
2. Recruitment: Technical Recruiter, Talent Acquisition, HR Manager (only if technical people are missing).
3. Founders/Management: CEO, Founder, President (good fallbacks for small companies).
4. Peers: DevOps Engineer, Backend Developer, SRE (fallbacks if no leadership found).

RULES:
- If NO perfect match from group 1 or 2 is found, pick the best candidate from group 3 or 4.
- NEVER return null if at least one real person with a LinkedIn profile is provided. 
- Avoid people in completely unrelated departments (Sales, Marketing, Legal) unless they are the only ones.

Return a JSON object with field "best" containing the chosen contact object (same fields as input).`

const BatchContactRankingPrompt = `Given this list of contacts at a %s company, rank ALL of them by suitability for cold-emailing about a DevOps/backend internship.

Priority order: 
1. Technical Leadership: CTO, VP Engineering, Engineering Manager, Tech Lead, Infrastructure Manager, IT Director.
2. Recruitment: Technical Recruiter, Talent Acquisition, HR Manager (only if technical people are missing).
3. Founders/Management: CEO, Founder, President (good fallbacks for small companies).
4. Peers: DevOps Engineer, Backend Developer, SRE (fallbacks if no leadership found).

RULES:
- Rank ALL contacts provided, do not skip any.
- Assign a score from 1-10 to each contact based on their suitability.
- Return the full list with scores, sorted by score descending.

Return a JSON object with field "ranked_contacts" containing the list of contacts with their scores.`

const IndividualProfilePrompt = `You are extracting information from a personal LinkedIn profile page.

Return a JSON object with:
- name: full name
- role: current job title and company
- email: personal work email if explicitly visible on the profile — empty string if not visible
- recent_posts: list of up to 3 topics or themes from their recent activity (empty list if none)
- background: 1-2 sentence summary of their professional background relevant to tech
- tech_stack: list of technologies mentioned on their profile`

const BatchProfileEnrichmentPrompt = `You are extracting information from multiple personal LinkedIn profile pages.

For each profile, return a JSON object with:
- name: full name
- role: current job title and company
- email: personal work email if explicitly visible — empty string if not visible
- recent_posts: list of up to 3 topics from their recent activity (empty list if none)
- background: 1-2 sentence summary of their professional background relevant to tech
- tech_stack: list of technologies mentioned on their profile

Return a JSON object with field "profiles" containing the list of extracted profiles in the same order as input.`

// ExtractPeopleFromPage extracts all individuals from a People tab markdown.
// Returns up to 5 candidates. Does NOT rank them.
func (c *Classifier) ExtractPeopleFromPage(ctx context.Context, markdown string, runID string) (PeoplePageData, error) {
	var result PeoplePageData
	req := llm.CompletionRequest{
		System: PeopleExtractionPrompt,
		User:   fmt.Sprintf("LinkedIn People tab content:\n\n%s", markdown),
	}
	err := c.llm.CompleteJSON(ctx, req, "extract_people", runID, &result)
	return result, err
}

// RankContacts picks the best contact from a list for a given company type.
func (c *Classifier) RankContacts(ctx context.Context, contacts []IndividualContact, companyType string, runID string) (*IndividualContact, error) {
	if len(contacts) == 0 {
		return nil, nil
	}
	if len(contacts) == 1 {
		return &contacts[0], nil
	}

	type rankResult struct {
		Best *IndividualContact `json:"best"`
	}
	var result rankResult

	contactsJSON, _ := json.Marshal(contacts)
	req := llm.CompletionRequest{
		System: fmt.Sprintf(ContactRankingPrompt, companyType),
		User:   fmt.Sprintf("Contacts:\n%s", string(contactsJSON)),
	}
	err := c.llm.CompleteJSON(ctx, req, "rank_contacts", runID, &result)
	return result.Best, err
}

// RankedContact represents a contact with a ranking score.
type RankedContact struct {
	IndividualContact
	Score int `json:"score"`
}

// BatchRankResult represents the result of batch contact ranking.
type BatchRankResult struct {
	RankedContacts []RankedContact `json:"ranked_contacts"`
}

// RankContactsBatch ranks all contacts in a single LLM call and returns them sorted by score.
func (c *Classifier) RankContactsBatch(ctx context.Context, contacts []IndividualContact, companyType string, runID string) (*IndividualContact, error) {
	if len(contacts) == 0 {
		return nil, nil
	}
	if len(contacts) == 1 {
		return &contacts[0], nil
	}

	var result BatchRankResult

	contactsJSON, _ := json.Marshal(contacts)
	req := llm.CompletionRequest{
		System: fmt.Sprintf(BatchContactRankingPrompt, companyType),
		User:   fmt.Sprintf("Contacts:\n%s", string(contactsJSON)),
	}
	err := c.llm.CompleteJSON(ctx, req, "rank_contacts_batch", runID, &result)
	if err != nil {
		return nil, err
	}

	// Return the top-ranked contact
	if len(result.RankedContacts) > 0 {
		return &result.RankedContacts[0].IndividualContact, nil
	}
	return nil, nil
}

// EnrichIndividualProfile fetches and extracts data from a personal /in/ profile.
func (c *Classifier) EnrichIndividualProfile(ctx context.Context, fetcher *scraper.CascadeFetcher, contact IndividualContact, runID string) (IndividualProfileData, error) {
	profileURL := normalizeLinkedInURL(contact.LinkedinURL)
	if profileURL == "" {
		return IndividualProfileData{Name: contact.Name, Role: contact.Role}, nil
	}

	res, err := fetcher.Fetch(ctx, profileURL)
	if err != nil {
		// Non-fatal: return what we already know
		return IndividualProfileData{Name: contact.Name, Role: contact.Role}, nil
	}

	var profile IndividualProfileData
	req := llm.CompletionRequest{
		System: IndividualProfilePrompt,
		User:   fmt.Sprintf("LinkedIn profile content:\n\n%s", res.ContentMD),
	}
	err = c.llm.CompleteJSON(ctx, req, "enrich_individual", runID, &profile)
	if err != nil {
		return IndividualProfileData{Name: contact.Name, Role: contact.Role}, nil
	}
	return profile, nil
}

// BatchProfileResult represents the result of batch profile enrichment.
type BatchProfileResult struct {
	Profiles []IndividualProfileData `json:"profiles"`
}

// EnrichProfilesBatch fetches and enriches multiple profiles in a single LLM call.
func (c *Classifier) EnrichProfilesBatch(ctx context.Context, fetcher *scraper.CascadeFetcher, contacts []IndividualContact, runID string) ([]IndividualProfileData, error) {
	if len(contacts) == 0 {
		return nil, nil
	}

	// Fetch all profiles and collect raw content
	profiles := make([]IndividualProfileData, len(contacts))
	rawContents := make([]string, len(contacts))
	for i, contact := range contacts {
		profileURL := normalizeLinkedInURL(contact.LinkedinURL)
		if profileURL == "" {
			profiles[i] = IndividualProfileData{Name: contact.Name, Role: contact.Role}
			continue
		}
		res, err := fetcher.Fetch(ctx, profileURL)
		if err != nil {
			profiles[i] = IndividualProfileData{Name: contact.Name, Role: contact.Role}
			continue
		}
		profiles[i] = IndividualProfileData{
			Name: contact.Name,
			Role: contact.Role,
		}
		rawContents[i] = res.ContentMD
	}

	// Build batch prompt with all profile contents
	var batchContent strings.Builder
	for i, content := range rawContents {
		if content != "" {
			batchContent.WriteString(fmt.Sprintf("\n--- Profile %d: %s ---\n%s\n", i+1, profiles[i].Name, content))
		}
	}

	if batchContent.Len() == 0 {
		return profiles, nil
	}

	var result BatchProfileResult
	req := llm.CompletionRequest{
		System: BatchProfileEnrichmentPrompt,
		User:   fmt.Sprintf("LinkedIn profile contents:\n%s", batchContent.String()),
	}
	err := c.llm.CompleteJSON(ctx, req, "enrich_profiles_batch", runID, &result)
	if err != nil {
		return profiles, nil
	}

	// Merge batch results into profiles
	for i, enriched := range result.Profiles {
		if i < len(profiles) {
			if enriched.Name != "" {
				profiles[i].Name = enriched.Name
			}
			if enriched.Role != "" {
				profiles[i].Role = enriched.Role
			}
			if enriched.Email != "" {
				profiles[i].Email = enriched.Email
			}
			profiles[i].RecentPosts = enriched.RecentPosts
			profiles[i].Background = enriched.Background
			profiles[i].TechStack = enriched.TechStack
		}
	}

	return profiles, nil
}

// The LLM occasionally returns relative paths (/in/...) or scheme-less URLs.
func normalizeLinkedInURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return "https://www.linkedin.com" + raw
	}
	return "https://www.linkedin.com/in/" + raw
}

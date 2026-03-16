package enricher

import (
	"context"
	"fmt"
	"jobhunter/internal/db"
	"jobhunter/internal/scraper"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var linkedinCompanyRe = regexp.MustCompile(
	`https?://(?:www\.)?linkedin\.com/company/([a-zA-Z0-9\-_%]+)`,
)

var nonAlphanumRe = regexp.MustCompile(`[^a-z0-9]+`)

type URLDiscoverer struct {
	fetcher    *scraper.CascadeFetcher
	classifier *Classifier
}

func NewURLDiscoverer(fetcher *scraper.CascadeFetcher, classifier *Classifier) *URLDiscoverer {
	return &URLDiscoverer{fetcher: fetcher, classifier: classifier}
}

func (d *URLDiscoverer) DiscoverURLs(ctx context.Context, comp db.Company, runID string) (string, string, error) {
	// 1. Search for Website
	websiteQuery := fmt.Sprintf("%s %s official website", comp.Name, comp.City.String)
	searchURL := fmt.Sprintf("https://duckduckgo.com/?q=%s", url.QueryEscape(websiteQuery))
	
	res, err := d.fetcher.Fetch(ctx, searchURL)
	var website string
	if err == nil {
		website = d.extractWebsiteURL(res.ContentMD, comp.Name)
		log.Printf("DDG Website Search: %s -> %s", websiteQuery, website)
	}

	time.Sleep(2 * time.Second) // Be nice to DDG

	// 2. Search for LinkedIn
	linkedinQuery := fmt.Sprintf("%s %s linkedin company", comp.Name, comp.City.String)
	searchURL = fmt.Sprintf("https://duckduckgo.com/?q=%s", url.QueryEscape(linkedinQuery))
	
	res, err = d.fetcher.Fetch(ctx, searchURL)
	var linkedin string
	if err == nil {
		linkedin = d.extractLinkedInURL(res.ContentMD)
		log.Printf("DDG LinkedIn Search: %s -> %s", linkedinQuery, linkedin)
	}

	// 3. Fallback: LLM extraction if regex failed or found only one
	if website == "" || linkedin == "" {
		time.Sleep(2 * time.Second)
		query := fmt.Sprintf("%s official website linkedin", comp.Name)
		searchURL = fmt.Sprintf("https://duckduckgo.com/?q=%s", url.QueryEscape(query))
		res, err = d.fetcher.Fetch(ctx, searchURL)
		if err == nil {
			lw, ll, err := d.classifier.ExtractURLsFromSearch(ctx, res.ContentMD, runID)
			if err == nil {
				if website == "" {
					website = lw
					log.Printf("LLM Discovery: Website -> %s", website)
				}
				if linkedin == "" {
					linkedin = ll
					log.Printf("LLM Discovery: LinkedIn -> %s", linkedin)
				}
			}
		}
	}

	// 4. Final Fallback: LinkedIn slug guessing (Fix D)
	if linkedin == "" {
		linkedin = guessLinkedInSlug(comp.Name)
		log.Printf("LinkedIn Guessing: %s -> %s", comp.Name, linkedin)
	}

	return website, linkedin, nil
}

func (d *URLDiscoverer) extractLinkedInURL(markdown string) string {
	match := linkedinCompanyRe.FindStringSubmatch(markdown)
	if len(match) < 2 {
		return ""
	}
	slug := strings.TrimRight(match[1], "/")
	return "https://www.linkedin.com/company/" + slug
}

// guessLinkedInSlug constructs a best-effort LinkedIn company URL from the
// company name. The result is unverified — callers must validate the fetch
// quality before saving the URL to the database.
func guessLinkedInSlug(name string) string {
	slug := strings.ToLower(name)
	slug = nonAlphanumRe.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return ""
	}
	return "https://www.linkedin.com/company/" + slug
}

func (d *URLDiscoverer) extractWebsiteURL(markdown string, companyName string) string {
	// Extract all links and find the most plausible one
	// This is a naive heuristic: find links that aren't common search engine/social noise
	re := regexp.MustCompile(`https?://[a-zA-Z0-9.\-]+\.[a-z]{2,}`)
	links := re.FindAllString(markdown, -1)

	noise := []string{
		"duckduckgo.com", "google.com", "bing.com", "linkedin.com", 
		"twitter.com", "facebook.com", "instagram.com", "youtube.com",
		"pappers.fr", "societe.com", "verif.com", "infogreffe.fr",
	}

	for _, link := range links {
		isNoise := false
		for _, n := range noise {
			if strings.Contains(link, n) {
				isNoise = true
				break
			}
		}
		if !isNoise {
			return link
		}
	}
	return ""
}

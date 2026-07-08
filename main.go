package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- Configuration ---
const apiURL = "https://api.github.com/graphql"

var readmeFile = ""

const startMarker = "<!-- START_STATS -->"
const endMarker = "<!-- END_STATS -->"

// --- Structs for Data & Cache ---

type CacheSchema struct {
	LastSync time.Time           `json:"last_sync"`
	PRs      map[string]CachedPR `json:"prs"` // Key = PR URL
}

type CachedPR struct {
	RepoName string `json:"repo_name"`
	Owner    string `json:"owner"`
	URL      string `json:"url"`
	State    string `json:"state"`
	Title    string `json:"title"`
}

type GraphQLPayload struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}
type GraphQLResponse struct {
	Data   Data           `json:"data"`
	Errors []GraphQLError `json:"errors"`
}
type GraphQLError struct {
	Message string `json:"message"`
}
type Data struct {
	User   User   `json:"user"`
	Search Search `json:"search"`
}
type User struct {
	CreatedAt string `json:"createdAt"`
}
type Search struct {
	Nodes    []PRNode `json:"nodes"`
	PageInfo PageInfo `json:"pageInfo"`
}
type PageInfo struct {
	EndCursor   string `json:"endCursor"`
	HasNextPage bool   `json:"hasNextPage"`
}
type PRNode struct {
	Title      string     `json:"title"`
	State      string     `json:"state"`
	Repository Repository `json:"repository"`
	URL        string     `json:"url"`
	UpdatedAt  string     `json:"updatedAt"`
}
type Repository struct {
	Name  string `json:"name"`
	Owner Owner  `json:"owner"`
	URL   string `json:"url"`
}
type Owner struct {
	Login string `json:"login"`
}

type RepoStats struct {
	RepoName string
	URL      string
	Total    int
	Merged   int
	Open     int
	Closed   int
}

// --- Main Execution ---
func main() {
	start := time.Now()

	token := os.Getenv("GH_STATS_TOKEN")
	if token == "" {
		fmt.Println("⚠️  Error: GH_STATS_TOKEN environment variable is not set.")
		os.Exit(1)
	}

	var username string
	forceScan := false
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--force" || arg == "-f" {
			forceScan = true
		} else if (arg == "--readme" || arg == "-r") && i+1 < len(args) {
			readmeFile = args[i+1]
			i++
		} else {
			username = extractUsername(arg)
		}
	}

	if username == "" {
		fmt.Println("Usage: go run main.go [--force | -f] [--readme | -r <path>] <username>")
		os.Exit(1)
	}

	// FIX: Generate unique cache filename for this user
	cacheFile := fmt.Sprintf("github_cache_%s.gob", username)

	// 1. Load Cache (Pass the specific filename)
	cache, err := loadCache(cacheFile)
	if err != nil {
		fmt.Printf("📦 No cache found for %s. Starting fresh...\n", username)
		cache = &CacheSchema{PRs: make(map[string]CachedPR)}
	} else {
		fmt.Printf("📦 Loaded cache for %s: %d PRs (Last sync: %s)\n", username, len(cache.PRs), cache.LastSync.Format(time.RFC822))
	}

	// 2. Decide: Full Scan or Incremental?
	var scanErr error
	if len(cache.PRs) == 0 || forceScan {
		fmt.Printf("🔍 Performing FULL HISTORY scan for: %s\n", username)
		if forceScan {
			cache.PRs = make(map[string]CachedPR)
		}
		scanErr = performFullScan(username, token, cache)
	} else {
		fmt.Printf("♻️  Checking for updates since %s...\n", cache.LastSync.Format(time.RFC822))
		scanErr = performIncrementalScan(username, token, cache)
	}

	if scanErr != nil {
		fmt.Printf("❌ Error fetching data from GitHub: %v\n", scanErr)
		os.Exit(1)
	}

	// 3. Save Cache (Update timestamp only on success)
	cache.LastSync = time.Now()
	saveCache(cacheFile, cache)

	// 4. Aggregate Stats from Cache
	stats := aggregateStats(cache, username)

	fmt.Printf("\n✅ Processed %d repositories in %s\n\n", len(stats), time.Since(start))

	// 5. Output
	printTerminalTable(stats)

	if readmeFile != "" {
		markdownOutput := generateMarkdownTable(stats)
		err = updateReadme(markdownOutput)
		if err == nil {
			fmt.Printf("\n📄 %s updated successfully.\n", readmeFile)
		} else {
			fmt.Printf("\n⚠️  Error: Could not update %s (%v)\n", readmeFile, err)
		}
	}
}

// --- Core Logic ---

func performFullScan(username, token string, cache *CacheSchema) error {
	startYear, err := getAccountCreationYear(username, token)
	if err != nil {
		fmt.Printf("⚠️  Could not fetch creation date, defaulting to 2015. Error: %v\n", err)
		startYear = 2015
	}

	currentYear := time.Now().Year()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var scanErr error

	for year := startYear; year <= currentYear; year++ {
		wg.Add(1)
		go func(y int) {
			defer wg.Done()
			nodes, err := fetchYearlyData(username, token, y)
			if err != nil {
				mu.Lock()
				scanErr = err
				mu.Unlock()
				return
			}

			mu.Lock()
			for _, node := range nodes {
				if node.URL == "" {
					continue
				}
				cache.PRs[node.URL] = CachedPR{
					RepoName: node.Repository.Name,
					Owner:    node.Repository.Owner.Login,
					URL:      node.Repository.URL,
					State:    node.State,
					Title:    node.Title,
				}
			}
			mu.Unlock()
			fmt.Printf("\r   ⚡ Fetched year %d...", y)
		}(year)
	}
	wg.Wait()
	return scanErr
}

func performIncrementalScan(username, token string, cache *CacheSchema) error {
	since := cache.LastSync.Add(-1 * time.Hour).Format(time.RFC3339)
	queryStr := fmt.Sprintf("author:%s is:pr updated:>%s", username, since)

	nodes, err := fetchGenericQuery(queryStr, token)
	if err != nil {
		return err
	}

	if len(nodes) > 0 {
		fmt.Printf("   ⬇️  Found %d new/updated PRs.\n", len(nodes))
		for _, node := range nodes {
			if node.URL == "" {
				continue
			}
			cache.PRs[node.URL] = CachedPR{
				RepoName: node.Repository.Name,
				Owner:    node.Repository.Owner.Login,
				URL:      node.Repository.URL,
				State:    node.State,
				Title:    node.Title,
			}
		}
	} else {
		fmt.Println("   ✨ No changes found.")
	}
	return nil
}

// --- Fetchers ---

func fetchYearlyData(username, token string, year int) ([]PRNode, error) {
	queryStr := fmt.Sprintf("author:%s is:pr created:%d-01-01..%d-12-31", username, year, year)
	return fetchGenericQuery(queryStr, token)
}

func fetchGenericQuery(queryStr, token string) ([]PRNode, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var allNodes []PRNode
	var cursor *string

	query := `
	query($search_query: String!, $cursor: String) {
	  search(query: $search_query, type: ISSUE, first: 100, after: $cursor) {
		pageInfo { endCursor, hasNextPage }
		nodes {
		  ... on PullRequest {
			title, state, url, updatedAt
			repository { name, owner { login }, url }
		  }
		}
	  }
	}`

	for {
		variables := map[string]interface{}{"search_query": queryStr, "cursor": cursor}
		payload := GraphQLPayload{Query: query, Variables: variables}
		reqBody, _ := json.Marshal(payload)

		req, _ := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub API returned status %s", resp.Status)
		}

		var result GraphQLResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		if len(result.Errors) > 0 {
			var errMsgs []string
			for _, e := range result.Errors {
				errMsgs = append(errMsgs, e.Message)
			}
			return nil, fmt.Errorf("GraphQL error: %s", strings.Join(errMsgs, "; "))
		}

		allNodes = append(allNodes, result.Data.Search.Nodes...)

		if !result.Data.Search.PageInfo.HasNextPage {
			break
		}
		nextCursor := result.Data.Search.PageInfo.EndCursor
		cursor = &nextCursor
	}
	return allNodes, nil
}

func getAccountCreationYear(username, token string) (int, error) {
	query := `query($user: String!) { user(login: $user) { createdAt } }`

	variables := map[string]interface{}{"user": username}
	payload := GraphQLPayload{Query: query, Variables: variables}
	reqBody, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", apiURL, bytes.NewBuffer(reqBody))
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("GitHub API returned status %s", resp.Status)
	}

	var result GraphQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	if len(result.Errors) > 0 {
		var errMsgs []string
		for _, e := range result.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		return 0, fmt.Errorf("GraphQL error: %s", strings.Join(errMsgs, "; "))
	}

	if result.Data.User.CreatedAt == "" {
		return 0, fmt.Errorf("user not found")
	}

	t, err := time.Parse(time.RFC3339, result.Data.User.CreatedAt)
	if err != nil {
		return 0, err
	}
	return t.Year(), nil
}

// --- Aggregation & Caching ---

func aggregateStats(cache *CacheSchema, username string) map[string]*RepoStats {
	stats := make(map[string]*RepoStats)

	for _, pr := range cache.PRs {
		if strings.EqualFold(pr.Owner, username) {
			continue
		}

		fullRepo := fmt.Sprintf("%s/%s", pr.Owner, pr.RepoName)
		if _, ok := stats[fullRepo]; !ok {
			repoURL := pr.URL
			if strings.Contains(repoURL, "/pull/") {
				repoURL = strings.Split(repoURL, "/pull/")[0]
			}
			stats[fullRepo] = &RepoStats{RepoName: fullRepo, URL: repoURL}
		}

		s := stats[fullRepo]
		s.Total++
		switch pr.State {
		case "MERGED":
			s.Merged++
		case "OPEN":
			s.Open++
		case "CLOSED":
			s.Closed++
		}
	}
	return stats
}

func loadCache(filename string) (*CacheSchema, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cache CacheSchema
	decoder := gob.NewDecoder(file)
	err = decoder.Decode(&cache)

	// Gob might return EOF if file is empty, handle gracefully
	if err != nil {
		return nil, err
	}

	return &cache, nil
}

func saveCache(filename string, cache *CacheSchema) {
	file, err := os.Create(filename) // Create overwrites the file
	if err != nil {
		fmt.Printf("⚠️  Warning: Could not save cache: %v\n", err)
		return
	}
	defer file.Close()

	encoder := gob.NewEncoder(file)
	if err := encoder.Encode(cache); err != nil {
		fmt.Printf("⚠️  Warning: Failed to encode cache: %v\n", err)
	}
}

// --- Formatters ---

func printTerminalTable(stats map[string]*RepoStats) {
	sorted := sortStats(stats)
	fmt.Printf("%-40s | %-9s | %-8s | %-6s | %-6s\n", "PROJECT / REPO", "TOTAL PRs", "MERGED", "OPEN", "CLOSED")
	fmt.Println(strings.Repeat("-", 85))

	for _, s := range sorted {
		displayName := s.RepoName
		if len(displayName) > 38 {
			displayName = displayName[:35] + "..."
		}

		clickableLink := fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", s.URL, displayName)

		padding := 40 - len(displayName)
		if padding < 0 {
			padding = 0
		}

		fmt.Printf("%s%s | %-9d | %-8d | %-6d | %-6d\n",
			clickableLink, strings.Repeat(" ", padding), s.Total, s.Merged, s.Open, s.Closed)
	}
}

func generateMarkdownTable(stats map[string]*RepoStats) string {
	sorted := sortStats(stats)
	var sb strings.Builder
	sb.WriteString("| Project | PRs | Merged | Open | Closed |\n")
	sb.WriteString("| :--- | :---: | :---: | :---: | :---: |\n")
	for _, s := range sorted {
		sb.WriteString(fmt.Sprintf("| [%s](%s) | %d | %d | %d | %d |\n",
			s.RepoName, s.URL, s.Total, s.Merged, s.Open, s.Closed))
	}
	return sb.String()
}

func sortStats(stats map[string]*RepoStats) []*RepoStats {
	var sorted []*RepoStats
	for _, s := range stats {
		sorted = append(sorted, s)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Total > sorted[j].Total
	})
	return sorted
}

func updateReadme(content string) error {
	input, err := os.ReadFile(readmeFile)
	if err != nil {
		return err
	}
	text := string(input)

	re := regexp.MustCompile(fmt.Sprintf(`(?s)%s.*?%s`, regexp.QuoteMeta(startMarker), regexp.QuoteMeta(endMarker)))
	if !re.MatchString(text) {
		return fmt.Errorf("markers not found")
	}

	newContent := fmt.Sprintf("%s\n%s\n%s", startMarker, content, endMarker)
	output := re.ReplaceAllString(text, newContent)

	return os.WriteFile(readmeFile, []byte(output), 0644)
}

func extractUsername(input string) string {
	input = strings.TrimSpace(input)
	if strings.Contains(input, "github.com/") {
		parts := strings.Split(input, "github.com/")
		if len(parts) > 1 {
			return strings.Trim(parts[1], "/")
		}
	}
	return input
}

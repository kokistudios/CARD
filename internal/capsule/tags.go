package capsule

import (
	"regexp"
	"strings"
)

// TagPrefix represents the semantic type of a tag.
type TagPrefix string

const (
	PrefixFile    TagPrefix = "file:"    // File path (e.g., file:src/auth/guard.ts)
	PrefixTable   TagPrefix = "table:"   // Database table (e.g., table:workspace_users)
	PrefixService TagPrefix = "service:" // Service/module (e.g., service:NotificationService)
	PrefixConcept TagPrefix = "concept:" // Domain concept (e.g., concept:authorization)
	PrefixAPI     TagPrefix = "api:"     // API endpoint (e.g., api:POST /notifications)
)

// AllPrefixes returns all valid tag prefixes.
func AllPrefixes() []TagPrefix {
	return []TagPrefix{PrefixFile, PrefixTable, PrefixService, PrefixConcept, PrefixAPI}
}

// HasPrefix checks if a tag already has a known prefix.
func HasPrefix(tag string) bool {
	for _, p := range AllPrefixes() {
		if strings.HasPrefix(tag, string(p)) {
			return true
		}
	}
	return false
}

// ParseTag extracts the prefix and value from a tag.
// Returns (prefix, value). If no prefix, returns ("", tag).
func ParseTag(tag string) (TagPrefix, string) {
	for _, p := range AllPrefixes() {
		if strings.HasPrefix(tag, string(p)) {
			return p, strings.TrimPrefix(tag, string(p))
		}
	}
	return "", tag
}

// InferPrefix applies inference rules to determine the appropriate prefix for an untyped tag.
// Rules (from PENSIEVE proposal):
//   - Starts with HTTP method (GET, POST, PUT, DELETE, PATCH) or contains /api/ → api:
//   - Contains / or common file extensions (.ts, .go, .py, .js, .tsx, .jsx, .rs, .java) → file:
//   - snake_case with common table patterns (ends with _users, _events, etc.) → table:
//   - PascalCase ending in Service, Controller, Handler, Repository, Manager → service:
//   - Otherwise → concept:
func InferPrefix(tag string) TagPrefix {
	// Already has prefix
	if HasPrefix(tag) {
		prefix, _ := ParseTag(tag)
		return prefix
	}

	tag = strings.TrimSpace(tag)
	if tag == "" {
		return PrefixConcept
	}

	// API detection FIRST: starts with HTTP method or contains /api/
	// (must come before file detection since APIs can contain /)
	httpMethods := []string{"GET ", "POST ", "PUT ", "DELETE ", "PATCH ", "HEAD ", "OPTIONS "}
	upperTag := strings.ToUpper(tag)
	for _, method := range httpMethods {
		if strings.HasPrefix(upperTag, method) {
			return PrefixAPI
		}
	}
	if strings.Contains(tag, "/api/") || strings.Contains(tag, "/v1/") || strings.Contains(tag, "/v2/") {
		return PrefixAPI
	}

	// File detection: contains / or has file extension
	fileExtensions := []string{".ts", ".tsx", ".js", ".jsx", ".go", ".py", ".rs", ".java", ".rb", ".php", ".cs", ".cpp", ".c", ".h", ".swift", ".kt", ".scala", ".vue", ".svelte", ".md", ".yaml", ".yml", ".json", ".toml", ".sql"}
	if strings.Contains(tag, "/") {
		return PrefixFile
	}
	for _, ext := range fileExtensions {
		if strings.HasSuffix(strings.ToLower(tag), ext) {
			return PrefixFile
		}
	}

	// Table detection: snake_case with table-like patterns
	tablePatterns := regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)+$`)
	tableSuffixes := []string{"_users", "_events", "_logs", "_records", "_items", "_entries", "_data", "_config", "_settings", "_sessions", "_tokens", "_keys", "_roles", "_permissions"}
	if tablePatterns.MatchString(tag) {
		for _, suffix := range tableSuffixes {
			if strings.HasSuffix(tag, suffix) {
				return PrefixTable
			}
		}
		// Also check if it just looks like a table name (snake_case, 2+ segments)
		if strings.Count(tag, "_") >= 1 && !strings.Contains(tag, ".") {
			return PrefixTable
		}
	}

	// Service detection: PascalCase ending with service-like suffix
	serviceSuffixes := []string{"Service", "Controller", "Handler", "Repository", "Manager", "Provider", "Factory", "Client", "Adapter", "Gateway", "Middleware", "Guard", "Interceptor", "Resolver"}
	for _, suffix := range serviceSuffixes {
		if strings.HasSuffix(tag, suffix) {
			// Verify it's PascalCase (starts with uppercase)
			if len(tag) > 0 && tag[0] >= 'A' && tag[0] <= 'Z' {
				return PrefixService
			}
		}
	}

	// Default: concept
	return PrefixConcept
}

// NormalizeTag applies the inferred prefix to a tag if it doesn't have one.
// Also strips backticks which may be present from markdown formatting.
func NormalizeTag(tag string) string {
	// Strip backticks (from markdown formatting)
	tag = strings.Trim(tag, "`")

	if HasPrefix(tag) {
		return tag
	}
	prefix := InferPrefix(tag)
	return string(prefix) + tag
}

// NormalizeTags applies prefixes to all tags in a slice.
func NormalizeTags(tags []string) []string {
	result := make([]string, len(tags))
	for i, tag := range tags {
		result[i] = NormalizeTag(tag)
	}
	return result
}

// StripPrefix removes the prefix from a tag, returning just the value.
func StripPrefix(tag string) string {
	_, value := ParseTag(tag)
	return value
}

// FilterByPrefix returns only tags with the specified prefix.
func FilterByPrefix(tags []string, prefix TagPrefix) []string {
	var result []string
	for _, tag := range tags {
		p, _ := ParseTag(tag)
		if p == prefix {
			result = append(result, tag)
		}
	}
	return result
}

// GetFileTags returns all file: tags from a slice.
func GetFileTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixFile)
}

// GetConceptTags returns all concept: tags from a slice.
func GetConceptTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixConcept)
}

// GetServiceTags returns all service: tags from a slice.
func GetServiceTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixService)
}

// GetTableTags returns all table: tags from a slice.
func GetTableTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixTable)
}

// GetAPITags returns all api: tags from a slice.
func GetAPITags(tags []string) []string {
	return FilterByPrefix(tags, PrefixAPI)
}

// synonymGroups defines related terms for fuzzy tag matching.
var synonymGroups = [][]string{
	{"auth", "authentication", "login", "signin", "sign-in", "oauth", "jwt", "token"},
	{"authz", "authorization", "permission", "permissions", "access", "access-control", "rbac", "acl"},
	{"db", "database", "sql", "postgres", "postgresql", "mysql", "sqlite", "mongo", "mongodb"},
	{"api", "endpoint", "endpoints", "route", "routes", "handler", "handlers", "controller"},
	{"test", "tests", "testing", "spec", "specs", "unit", "integration", "e2e"},
	{"config", "configuration", "settings", "options", "preferences", "env", "environment"},
	{"cache", "caching", "redis", "memcache", "memoize", "memoization"},
	{"queue", "queues", "job", "jobs", "worker", "workers", "async", "background"},
	{"log", "logging", "logger", "logs", "debug", "trace", "audit"},
	{"error", "errors", "exception", "exceptions", "failure", "failures", "handling"},
	{"security", "secure", "vulnerability", "vulnerabilities", "xss", "csrf", "injection"},
	{"rate", "ratelimit", "rate-limit", "throttle", "throttling", "limit", "limiting"},
	{"validate", "validation", "validator", "validators", "schema", "sanitize"},
	{"user", "users", "account", "accounts", "profile", "profiles", "member"},
	{"notify", "notification", "notifications", "alert", "alerts", "email", "sms"},
}

// GetSynonyms returns related terms for a given word.
func GetSynonyms(word string) []string {
	wordLower := strings.ToLower(word)
	for _, group := range synonymGroups {
		for _, term := range group {
			if term == wordLower {
				// Return all other terms in the group
				var synonyms []string
				for _, t := range group {
					if t != wordLower {
						synonyms = append(synonyms, t)
					}
				}
				return synonyms
			}
		}
	}
	return nil
}

// ExpandWithSynonyms returns the query plus all synonyms.
func ExpandWithSynonyms(query string) []string {
	result := []string{query}
	synonyms := GetSynonyms(query)
	result = append(result, synonyms...)
	return result
}

// MatchesTagQuery checks if any tag matches a query, handling prefix semantics.
// If the query has a prefix, only matches tags with that prefix.
// If the query has no prefix, matches against the value portion of all tags.
func MatchesTagQuery(tags []string, query string) bool {
	queryPrefix, queryValue := ParseTag(query)
	queryValueLower := strings.ToLower(queryValue)

	for _, tag := range tags {
		tagPrefix, tagValue := ParseTag(tag)
		tagValueLower := strings.ToLower(tagValue)

		if queryPrefix != "" {
			// Query has prefix: must match prefix and contain value
			if tagPrefix == queryPrefix && strings.Contains(tagValueLower, queryValueLower) {
				return true
			}
		} else {
			// Query has no prefix: match against value of any tag
			if strings.Contains(tagValueLower, queryValueLower) {
				return true
			}
		}
	}
	return false
}

// MatchesTagQueryWithSynonyms checks if any tag matches a query or its synonyms.
// This enables fuzzy matching like "auth" matching "authentication".
func MatchesTagQueryWithSynonyms(tags []string, query string) bool {
	// First try exact match
	if MatchesTagQuery(tags, query) {
		return true
	}

	// Try synonym expansion
	_, queryValue := ParseTag(query)
	synonyms := GetSynonyms(queryValue)
	for _, syn := range synonyms {
		if MatchesTagQuery(tags, syn) {
			return true
		}
	}

	return false
}

package capsule

import (
	"regexp"
	"strings"
)

type TagPrefix string

const (
	PrefixFile    TagPrefix = "file:"
	PrefixTable   TagPrefix = "table:"
	PrefixService TagPrefix = "service:"
	PrefixConcept TagPrefix = "concept:"
	PrefixAPI     TagPrefix = "api:"
)

func AllPrefixes() []TagPrefix {
	return []TagPrefix{PrefixFile, PrefixTable, PrefixService, PrefixConcept, PrefixAPI}
}

func HasPrefix(tag string) bool {
	for _, p := range AllPrefixes() {
		if strings.HasPrefix(tag, string(p)) {
			return true
		}
	}
	return false
}

func ParseTag(tag string) (TagPrefix, string) {
	for _, p := range AllPrefixes() {
		if strings.HasPrefix(tag, string(p)) {
			return p, strings.TrimPrefix(tag, string(p))
		}
	}
	return "", tag
}

func InferPrefix(tag string) TagPrefix {
	if HasPrefix(tag) {
		prefix, _ := ParseTag(tag)
		return prefix
	}

	tag = strings.TrimSpace(tag)
	if tag == "" {
		return PrefixConcept
	}

	// API detection must come before file detection since APIs can contain /
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

	fileExtensions := []string{".ts", ".tsx", ".js", ".jsx", ".go", ".py", ".rs", ".java", ".rb", ".php", ".cs", ".cpp", ".c", ".h", ".swift", ".kt", ".scala", ".vue", ".svelte", ".md", ".yaml", ".yml", ".json", ".toml", ".sql"}
	if strings.Contains(tag, "/") {
		return PrefixFile
	}
	for _, ext := range fileExtensions {
		if strings.HasSuffix(strings.ToLower(tag), ext) {
			return PrefixFile
		}
	}

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

	serviceSuffixes := []string{"Service", "Controller", "Handler", "Repository", "Manager", "Provider", "Factory", "Client", "Adapter", "Gateway", "Middleware", "Guard", "Interceptor", "Resolver"}
	for _, suffix := range serviceSuffixes {
		if strings.HasSuffix(tag, suffix) {
			if len(tag) > 0 && tag[0] >= 'A' && tag[0] <= 'Z' {
				return PrefixService
			}
		}
	}

	return PrefixConcept
}

func NormalizeTag(tag string) string {
	tag = strings.Trim(tag, "`")

	if HasPrefix(tag) {
		return tag
	}
	prefix := InferPrefix(tag)
	return string(prefix) + tag
}

func NormalizeTags(tags []string) []string {
	result := make([]string, len(tags))
	for i, tag := range tags {
		result[i] = NormalizeTag(tag)
	}
	return result
}

func StripPrefix(tag string) string {
	_, value := ParseTag(tag)
	return value
}

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

func GetFileTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixFile)
}

func GetConceptTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixConcept)
}

func GetServiceTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixService)
}

func GetTableTags(tags []string) []string {
	return FilterByPrefix(tags, PrefixTable)
}

func GetAPITags(tags []string) []string {
	return FilterByPrefix(tags, PrefixAPI)
}

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

func GetSynonyms(word string) []string {
	wordLower := strings.ToLower(word)
	for _, group := range synonymGroups {
		for _, term := range group {
			if term == wordLower {
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

func ExpandWithSynonyms(query string) []string {
	result := []string{query}
	synonyms := GetSynonyms(query)
	result = append(result, synonyms...)
	return result
}

func MatchesTagQuery(tags []string, query string) bool {
	queryPrefix, queryValue := ParseTag(query)
	queryValueLower := strings.ToLower(queryValue)

	for _, tag := range tags {
		tagPrefix, tagValue := ParseTag(tag)
		tagValueLower := strings.ToLower(tagValue)

		if queryPrefix != "" {
			if tagPrefix == queryPrefix && strings.Contains(tagValueLower, queryValueLower) {
				return true
			}
		} else {
			if strings.Contains(tagValueLower, queryValueLower) {
				return true
			}
		}
	}
	return false
}

func MatchesTagQueryWithSynonyms(tags []string, query string) bool {
	if MatchesTagQuery(tags, query) {
		return true
	}

	_, queryValue := ParseTag(query)
	synonyms := GetSynonyms(queryValue)
	for _, syn := range synonyms {
		if MatchesTagQuery(tags, syn) {
			return true
		}
	}

	return false
}

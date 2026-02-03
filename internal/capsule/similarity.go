package capsule

import (
	"regexp"
	"strings"
	"unicode"
)

// SimilarityResult captures the outcome of a similarity check.
type SimilarityResult struct {
	Similar         []SimilarMatch  `json:"similar,omitempty"`
	Contradicts     []Contradiction `json:"contradicts,omitempty"`
	SuggestedAction string          `json:"suggested_action"` // "create", "supersedes:<id>", "duplicate_of:<id>"
}

// SimilarMatch represents a capsule that is semantically similar to a proposed decision.
type SimilarMatch struct {
	CapsuleID        string `json:"capsule_id"`
	Question         string `json:"question"`
	Choice           string `json:"choice"`
	Phase            string `json:"phase"`
	SimilarityReason string `json:"similarity_reason"`
	Confidence       string `json:"confidence"` // "high", "medium", "low"
}

// Contradiction represents a prior active decision that conflicts with a proposed decision.
type Contradiction struct {
	CapsuleID          string `json:"capsule_id"`
	Question           string `json:"question"`
	Choice             string `json:"choice"`
	SessionID          string `json:"session_id"`
	SessionDescription string `json:"session_description,omitempty"`
	Reason             string `json:"reason"`
}

// FastSimilarityCheck performs quick non-LLM checks for similar decisions.
// This is used for implementation/context decisions where speed (<50ms) is important.
// It checks:
// 1. Exact match on normalized question text
// 2. Keyword overlap using Jaccard similarity (threshold: 60%)
//
// Returns nil if no similar decisions found.
func FastSimilarityCheck(existing []Capsule, proposed Capsule) *SimilarityResult {
	result := &SimilarityResult{
		SuggestedAction: "create",
	}

	normalizedProposed := normalizeText(proposed.Question)
	proposedKeywords := extractKeywords(proposed.Question)

	for _, c := range existing {
		// Skip invalidated capsules
		if c.Status == StatusInvalidated {
			continue
		}

		normalizedExisting := normalizeText(c.Question)

		// Check 1: Exact match on normalized text
		if normalizedProposed == normalizedExisting {
			result.Similar = append(result.Similar, SimilarMatch{
				CapsuleID:        c.ID,
				Question:         c.Question,
				Choice:           c.Choice,
				Phase:            c.Phase,
				SimilarityReason: "Exact match on normalized question text",
				Confidence:       "high",
			})
			result.SuggestedAction = "duplicate_of:" + c.ID
			continue
		}

		// Check 2: Keyword overlap (Jaccard similarity)
		existingKeywords := extractKeywords(c.Question)
		similarity := jaccardSimilarity(proposedKeywords, existingKeywords)

		if similarity >= 0.6 {
			confidence := "medium"
			if similarity >= 0.8 {
				confidence = "high"
			}
			result.Similar = append(result.Similar, SimilarMatch{
				CapsuleID:        c.ID,
				Question:         c.Question,
				Choice:           c.Choice,
				Phase:            c.Phase,
				SimilarityReason: "High keyword overlap in question text",
				Confidence:       confidence,
			})
			if result.SuggestedAction == "create" && confidence == "high" {
				result.SuggestedAction = "duplicate_of:" + c.ID
			}
		}
	}

	if len(result.Similar) == 0 {
		return nil
	}
	return result
}

// FastContradictionCheck performs quick non-LLM checks for contradicting decisions.
// Looks for decisions with similar questions but different choices.
// Returns nil if no contradictions found.
func FastContradictionCheck(allActive []Capsule, proposed Capsule) *SimilarityResult {
	result := &SimilarityResult{
		SuggestedAction: "create",
	}

	proposedKeywords := extractKeywords(proposed.Question)
	proposedChoiceKeywords := extractKeywords(proposed.Choice)

	for _, c := range allActive {
		// Skip invalidated capsules
		if c.Status == StatusInvalidated {
			continue
		}

		// Check for similar question
		existingKeywords := extractKeywords(c.Question)
		questionSimilarity := jaccardSimilarity(proposedKeywords, existingKeywords)

		if questionSimilarity >= 0.5 {
			// Similar question - check if choices differ significantly
			existingChoiceKeywords := extractKeywords(c.Choice)
			choiceSimilarity := jaccardSimilarity(proposedChoiceKeywords, existingChoiceKeywords)

			// If questions are similar but choices are different, it might be a contradiction
			if choiceSimilarity < 0.4 {
				result.Contradicts = append(result.Contradicts, Contradiction{
					CapsuleID: c.ID,
					Question:  c.Question,
					Choice:    c.Choice,
					SessionID: c.SessionID,
					Reason:    "Similar question with significantly different choice",
				})
				result.SuggestedAction = "supersedes:" + c.ID
			}
		}
	}

	if len(result.Contradicts) == 0 {
		return nil
	}
	return result
}

// MergeSimilarityResults combines two similarity results into one.
func MergeSimilarityResults(a, b *SimilarityResult) *SimilarityResult {
	if a == nil && b == nil {
		return nil
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	result := &SimilarityResult{
		Similar:         append(a.Similar, b.Similar...),
		Contradicts:     append(a.Contradicts, b.Contradicts...),
		SuggestedAction: a.SuggestedAction,
	}

	// Prefer supersedes over duplicate_of over create
	if strings.HasPrefix(b.SuggestedAction, "supersedes:") {
		result.SuggestedAction = b.SuggestedAction
	} else if strings.HasPrefix(b.SuggestedAction, "duplicate_of:") && !strings.HasPrefix(a.SuggestedAction, "supersedes:") {
		result.SuggestedAction = b.SuggestedAction
	}

	return result
}

// normalizeText converts text to lowercase, removes punctuation, and normalizes whitespace.
func normalizeText(text string) string {
	// Convert to lowercase
	text = strings.ToLower(text)

	// Remove punctuation
	text = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return ' '
		}
		return r
	}, text)

	// Normalize whitespace
	words := strings.Fields(text)
	return strings.Join(words, " ")
}

// extractKeywords extracts significant keywords from text.
// Removes common stop words and returns unique lowercase terms.
func extractKeywords(text string) map[string]bool {
	// Common stop words to ignore
	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
		"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
		"with": true, "by": true, "from": true, "as": true, "is": true, "was": true,
		"are": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true, "did": true,
		"will": true, "would": true, "could": true, "should": true, "may": true, "might": true,
		"this": true, "that": true, "these": true, "those": true,
		"i": true, "we": true, "you": true, "he": true, "she": true, "it": true, "they": true,
		"what": true, "which": true, "who": true, "when": true, "where": true, "why": true, "how": true,
		"use": true, "using": true, "used": true,
	}

	// Normalize and split
	text = strings.ToLower(text)
	// Split on non-alphanumeric characters
	re := regexp.MustCompile(`[^a-z0-9]+`)
	words := re.Split(text, -1)

	keywords := make(map[string]bool)
	for _, word := range words {
		word = strings.TrimSpace(word)
		if len(word) > 2 && !stopWords[word] {
			keywords[word] = true
		}
	}
	return keywords
}

// jaccardSimilarity calculates the Jaccard similarity coefficient between two sets.
// Returns a value between 0 (no overlap) and 1 (identical sets).
func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	// Calculate intersection
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}

	// Calculate union
	union := make(map[string]bool)
	for k := range a {
		union[k] = true
	}
	for k := range b {
		union[k] = true
	}

	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

// Note: FullSimilarityCheck with LLM-based semantic analysis will be implemented
// when we add the card_decision MCP handler. It requires access to Claude for
// semantic comparison and will be called for architectural decisions only.

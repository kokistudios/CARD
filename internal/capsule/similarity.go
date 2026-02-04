package capsule

import (
	"regexp"
	"strings"
	"unicode"
)

type SimilarityResult struct {
	Similar         []SimilarMatch  `json:"similar,omitempty"`
	Contradicts     []Contradiction `json:"contradicts,omitempty"`
	SuggestedAction string          `json:"suggested_action"` // "create", "supersedes:<id>", "duplicate_of:<id>"
}

type SimilarMatch struct {
	CapsuleID        string `json:"capsule_id"`
	Question         string `json:"question"`
	Choice           string `json:"choice"`
	Phase            string `json:"phase"`
	SimilarityReason string `json:"similarity_reason"`
	Confidence       string `json:"confidence"` // "high", "medium", "low"
}

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
		if c.Status == StatusInvalidated {
			continue
		}

		normalizedExisting := normalizeText(c.Question)

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

func FastContradictionCheck(allActive []Capsule, proposed Capsule) *SimilarityResult {
	result := &SimilarityResult{
		SuggestedAction: "create",
	}

	proposedKeywords := extractKeywords(proposed.Question)
	proposedChoiceKeywords := extractKeywords(proposed.Choice)

	for _, c := range allActive {
		if c.Status == StatusInvalidated {
			continue
		}

		existingKeywords := extractKeywords(c.Question)
		questionSimilarity := jaccardSimilarity(proposedKeywords, existingKeywords)

		if questionSimilarity >= 0.5 {
			existingChoiceKeywords := extractKeywords(c.Choice)
			choiceSimilarity := jaccardSimilarity(proposedChoiceKeywords, existingChoiceKeywords)

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

	if strings.HasPrefix(b.SuggestedAction, "supersedes:") {
		result.SuggestedAction = b.SuggestedAction
	} else if strings.HasPrefix(b.SuggestedAction, "duplicate_of:") && !strings.HasPrefix(a.SuggestedAction, "supersedes:") {
		result.SuggestedAction = b.SuggestedAction
	}

	return result
}

func normalizeText(text string) string {
	text = strings.ToLower(text)

	text = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			return ' '
		}
		return r
	}, text)

	words := strings.Fields(text)
	return strings.Join(words, " ")
}

func extractKeywords(text string) map[string]bool {
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

	text = strings.ToLower(text)
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

func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}

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


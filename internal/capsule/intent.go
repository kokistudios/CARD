package capsule

import (
	"regexp"
	"strings"
)

type InvalidationIntent struct {
	Detected        bool
	Confidence      string // "high", "medium", "low"
	MatchedPattern  string
	SuggestedAction string
	CapsuleID       string // If we can identify the specific capsule
}

var invalidationPatterns = []struct {
	pattern    *regexp.Regexp
	confidence string
	action     string
}{
	{
		regexp.MustCompile(`(?i)\b(this|that|the previous|earlier|old)\s+(decision|approach|choice|implementation)\s+(was|is)\s+(wrong|incorrect|bad|flawed|mistaken)`),
		"high",
		"invalidate_and_supersede",
	},
	{
		regexp.MustCompile(`(?i)\bwrong\s+(about|decision|approach|choice)\b`),
		"high",
		"invalidate_and_supersede",
	},
	{
		regexp.MustCompile(`(?i)\b(should\s+not\s+have|shouldn't\s+have)\s+(used|chosen|implemented|picked)`),
		"high",
		"invalidate_and_supersede",
	},
	{
		regexp.MustCompile(`(?i)\b(invalidate|supersede|replace|overturn)\s+(this|that|the)\s+(decision|choice)`),
		"high",
		"invalidate_and_supersede",
	},
	{
		regexp.MustCompile(`(?i)\bthat\s+was\s+a\s+mistake\b`),
		"high",
		"invalidate_and_supersede",
	},
	{
		regexp.MustCompile(`(?i)\b(turns\s+out|it\s+turns\s+out)\s+.*\s+(was|is)\s+(wrong|incorrect|bad)`),
		"high",
		"invalidate_and_supersede",
	},
	{
		regexp.MustCompile(`(?i)\b(revert|undo|rollback|go\s+back\s+to|switch\s+back)\b`),
		"medium",
		"challenge_or_invalidate",
	},
	{
		regexp.MustCompile(`(?i)\bactually.*\bshould\s+(use|implement|choose)`),
		"medium",
		"challenge_or_invalidate",
	},
	{
		regexp.MustCompile(`(?i)\binstead.*\bshould\s+(use|implement|choose)`),
		"medium",
		"challenge_or_invalidate",
	},
	{
		regexp.MustCompile(`(?i)\bchanging\s+(my|our)\s+mind\b`),
		"medium",
		"challenge_or_invalidate",
	},
	{
		regexp.MustCompile(`(?i)\b(reconsider|rethink|revisit)\s+(this|that|the)\s+(decision|approach|choice)`),
		"medium",
		"challenge_or_invalidate",
	},
	{
		regexp.MustCompile(`(?i)\b(didn't|did\s+not)\s+work\s+(out|well|as\s+expected)`),
		"medium",
		"challenge_or_invalidate",
	},
	{
		regexp.MustCompile(`(?i)\bwhy\s+did\s+we\s+(choose|pick|select|use)`),
		"low",
		"review_and_discuss",
	},
	{
		regexp.MustCompile(`(?i)\b(not\s+sure|unsure|uncertain)\s+(about|if)\s+(this|that|the)\s+(decision|approach|choice)`),
		"low",
		"review_and_discuss",
	},
	{
		regexp.MustCompile(`(?i)\b(better|alternative|different)\s+(approach|option|way)`),
		"low",
		"review_and_discuss",
	},
}

func DetectInvalidationIntent(text string) *InvalidationIntent {
	text = strings.ToLower(text)

	for _, p := range invalidationPatterns {
		if p.pattern.MatchString(text) {
			return &InvalidationIntent{
				Detected:        true,
				Confidence:      p.confidence,
				MatchedPattern:  p.pattern.String(),
				SuggestedAction: p.action,
			}
		}
	}

	return &InvalidationIntent{
		Detected: false,
	}
}

func DetectInvalidationIntentWithContext(text string, referencedCapsuleID string) *InvalidationIntent {
	intent := DetectInvalidationIntent(text)
	if intent.Detected && referencedCapsuleID != "" {
		intent.CapsuleID = referencedCapsuleID
	}
	return intent
}

func (i *InvalidationIntent) InvalidationPrompt() string {
	if !i.Detected {
		return ""
	}

	switch i.Confidence {
	case "high":
		if i.CapsuleID != "" {
			return "It sounds like you want to invalidate decision " + i.CapsuleID + ". Mark as invalidated? [Y/n]"
		}
		return "It sounds like you want to invalidate a prior decision. Which decision should be marked as invalidated?"
	case "medium":
		if i.CapsuleID != "" {
			return "This might invalidate decision " + i.CapsuleID + ". Would you like to: (1) Invalidate and supersede, (2) Add a challenge note, or (3) Continue without action?"
		}
		return "This might affect a prior decision. Would you like to note this as a challenge to the original decision?"
	case "low":
		return "Note: This discussion references prior decisions. If you're reconsidering a choice, consider using 'card capsule invalidate <id>' to maintain decision history."
	}

	return ""
}

func (i *InvalidationIntent) ActionDescription() string {
	switch i.SuggestedAction {
	case "invalidate_and_supersede":
		return "Invalidate the prior decision and create a new superseding capsule"
	case "challenge_or_invalidate":
		return "Either add a challenge record or invalidate the decision"
	case "review_and_discuss":
		return "Review the decision context before taking action"
	default:
		return "No action suggested"
	}
}

func (i *InvalidationIntent) ConfidenceLevel() int {
	switch i.Confidence {
	case "high":
		return 90
	case "medium":
		return 60
	case "low":
		return 30
	default:
		return 0
	}
}

func ExtractCapsuleReferences(text string) []string {
	var refs []string

	idPattern := regexp.MustCompile(`\b(\d{8}-[a-z0-9-]+-[a-f0-9]{8})\b`)
	matches := idPattern.FindAllString(text, -1)
	refs = append(refs, matches...)

	decisionPattern := regexp.MustCompile(`(?i)\b(?:decision|capsule)\s+([a-zA-Z][a-zA-Z0-9-]*[a-zA-Z0-9])`)
	decMatches := decisionPattern.FindAllStringSubmatch(text, -1)
	for _, m := range decMatches {
		if m[1] != "" {
			refs = append(refs, m[1])
		}
	}

	seen := make(map[string]bool)
	var unique []string
	for _, ref := range refs {
		if !seen[ref] {
			seen[ref] = true
			unique = append(unique, ref)
		}
	}

	return unique
}

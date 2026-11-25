package utils

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/open-ug/conveyor/pkg/types"
)

// pipelineNamePattern is deliberately strict: a pipeline name ends up in a
// BadgerDB key ("/pipelines/<name>") and is used in URLs, so only characters
// that are safe in both are accepted. Existing names that do not match stay
// readable and deletable; they only fail when written again.
var pipelineNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// ValidatePipelineDefinition checks a pipeline definition and returns every
// problem found, ordered by field path (for example "name", "steps[0].driver").
// Unlike a fail-fast validator it reports all issues so a client can fix a
// definition in one round trip.
//
// A driver name is embedded in the NATS subject
// "drivers.<driver>.resources.<type>", so ".", "*" and ">" (subject token
// separators and wildcards) and whitespace are rejected. The same driver may
// appear in several steps; that is a valid retry/status pattern.
func ValidatePipelineDefinition(p types.Pipeline) []string {
	var issues []string

	if p.Name == "" {
		issues = append(issues, "name: must not be empty")
	} else if !pipelineNamePattern.MatchString(p.Name) {
		issues = append(issues, "name: must contain only lowercase letters, digits and hyphens")
	}

	if len(p.Steps) == 0 {
		issues = append(issues, "steps: must contain at least one step")
		return issues
	}

	seenIDs := make(map[string]int, len(p.Steps))
	for i, step := range p.Steps {
		path := fmt.Sprintf("steps[%d]", i)

		switch {
		case step.ID == "":
			issues = append(issues, path+".id: must not be empty")
		default:
			if first, ok := seenIDs[step.ID]; ok {
				issues = append(issues, fmt.Sprintf("%s.id: duplicate of steps[%d].id (%q)", path, first, step.ID))
			} else {
				seenIDs[step.ID] = i
			}
		}

		switch {
		case step.Driver == "":
			issues = append(issues, path+".driver: must not be empty")
		case strings.ContainsAny(step.Driver, ".*>") || strings.IndexFunc(step.Driver, unicode.IsSpace) >= 0:
			issues = append(issues, path+".driver: must not contain '.', '*', '>' or whitespace")
		}
	}

	return issues
}

package utils_test

import (
	"testing"

	"github.com/open-ug/conveyor/internal/utils"
	"github.com/open-ug/conveyor/pkg/types"
	"github.com/stretchr/testify/assert"
)

func TestValidatePipelineDefinition_Valid(t *testing.T) {
	// The same driver in two steps is valid (status-style reporting steps).
	pipeline := types.Pipeline{
		Name:     "gitops",
		Resource: "gitop",
		Steps: []types.Step{
			{ID: "1", Driver: "buildpacks"},
			{ID: "2", Driver: "status"},
			{ID: "3", Driver: "docker"},
			{ID: "4", Driver: "status"},
		},
	}
	assert.Empty(t, utils.ValidatePipelineDefinition(pipeline))
}

func TestValidatePipelineDefinition_ReportsAllIssues(t *testing.T) {
	pipeline := types.Pipeline{
		Name: "Bad Name!",
		Steps: []types.Step{
			{ID: "", Driver: "drivers.with.dot"},
			{ID: "a", Driver: ""},
			{ID: "a", Driver: "status*"},
		},
	}
	issues := utils.ValidatePipelineDefinition(pipeline)
	assert.Equal(t, []string{
		"name: must contain only lowercase letters, digits and hyphens",
		"steps[0].id: must not be empty",
		"steps[0].driver: must not contain '.', '*', '>' or whitespace",
		"steps[1].driver: must not be empty",
		"steps[2].id: duplicate of steps[1].id (\"a\")",
		"steps[2].driver: must not contain '.', '*', '>' or whitespace",
	}, issues)
}

func TestValidatePipelineDefinition_EmptyNameAndSteps(t *testing.T) {
	issues := utils.ValidatePipelineDefinition(types.Pipeline{})
	assert.Equal(t, []string{
		"name: must not be empty",
		"steps: must contain at least one step",
	}, issues)
}

func TestValidatePipelineDefinition_WhitespaceAndWildcards(t *testing.T) {
	for _, driver := range []string{"has space", "has\ttab", "wild>", "star*"} {
		issues := utils.ValidatePipelineDefinition(types.Pipeline{
			Name:  "p",
			Steps: []types.Step{{ID: "1", Driver: driver}},
		})
		assert.Len(t, issues, 1, "driver %q should be rejected", driver)
		assert.Contains(t, issues[0], "steps[0].driver")
	}
}

func TestValidatePipelineDefinition_NameCharset(t *testing.T) {
	for _, name := range []string{"gitops", "build-1", "a"} {
		assert.Empty(t, utils.ValidatePipelineDefinition(types.Pipeline{
			Name:  name,
			Steps: []types.Step{{ID: "1", Driver: "d"}},
		}), "name %q should be accepted", name)
	}
	for _, name := range []string{"GitOps", "build_1", "build.1", "build/1", " build"} {
		issues := utils.ValidatePipelineDefinition(types.Pipeline{
			Name:  name,
			Steps: []types.Step{{ID: "1", Driver: "d"}},
		})
		assert.NotEmpty(t, issues, "name %q should be rejected", name)
	}
}

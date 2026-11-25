package handlers

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/dgraph-io/badger/v4"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
	"github.com/open-ug/conveyor/internal/models"
	utils "github.com/open-ug/conveyor/internal/utils"
	"github.com/open-ug/conveyor/pkg/types"
)

// maxReportedReferences caps how many referencing resources a 409 response
// lists. The count in "resource_count" stays exact.
const maxReportedReferences = 10

type PipelineHandler struct {
	Model                   *models.PipelineModel
	NatsCon                 *nats.Conn
	ResourceDefinitionModel *models.ResourceDefinitionModel
	ResourceModel           *models.ResourceModel
}

func NewPipelineHandler(natsCon *nats.Conn, db *badger.DB) *PipelineHandler {
	return &PipelineHandler{
		Model:                   models.NewPipelineModel(db),
		NatsCon:                 natsCon,
		ResourceDefinitionModel: models.NewResourceDefinitionModel(db),
		ResourceModel:           models.NewResourceModel(db),
	}
}

// issueResponse reports a bad definition. "error" keeps the first issue so
// older clients still read a single explanation; "errors" carries them all.
func issueResponse(c *fiber.Ctx, status int, issues []string) error {
	return c.Status(status).JSON(fiber.Map{
		"error":  issues[0],
		"errors": issues,
	})
}

// conflictingResources returns the names of resources that reference a
// pipeline, plus the exact count. Resources are scanned by the pipeline's
// resource type because that is the only index resources are stored under.
func (h *PipelineHandler) conflictingResources(resourceType, pipelineName string) ([]string, int, error) {
	resources, err := h.ResourceModel.List(resourceType)
	if err != nil {
		return nil, 0, err
	}
	var names []string
	for _, resource := range resources {
		if resource.Pipeline == pipelineName {
			names = append(names, resource.Name)
		}
	}
	sort.Strings(names)
	return names, len(names), nil
}

// conflictResponse reports a 409 with up to maxReportedReferences resource
// names so a client can see what blocks or is affected by the change.
func conflictResponse(c *fiber.Ctx, message string, names []string) error {
	reported := names
	if len(reported) > maxReportedReferences {
		reported = reported[:maxReportedReferences]
	}
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{
		"error":          message,
		"resources":      reported,
		"resource_count": len(names),
	})
}

// CreatePipeline creates a new pipeline
// @Summary Create a new pipeline
// @Description Create a new pipeline with the specified configuration
// @Tags pipelines
// @Accept json
// @Produce json
// @Param pipeline body types.Pipeline true "Pipeline object"
// @Success 201 {object} types.Pipeline "Pipeline created successfully"
// @Failure 400 {object} map[string]interface{} "Bad request - Invalid payload or definition"
// @Failure 404 {object} map[string]interface{} "Resource definition not found"
// @Failure 409 {object} map[string]interface{} "Pipeline with this name already exists"
// @Failure 500 {object} map[string]interface{} "Internal server error"
// @Router /pipelines [post]
func (h *PipelineHandler) CreatePipeline(c *fiber.Ctx) error {
	var pipeline types.Pipeline
	if err := c.BodyParser(&pipeline); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request payload",
		})
	}

	if issues := utils.ValidatePipelineDefinition(pipeline); len(issues) > 0 {
		return issueResponse(c, fiber.StatusBadRequest, issues)
	}

	// A name in use is always reported as a conflict, before other checks, so
	// a duplicate create never falls through to a different error.
	if _, err := h.Model.GetPipeline(pipeline.Name); err == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": fmt.Sprintf("Pipeline %s already exists", pipeline.Name),
		})
	} else if !errors.Is(err, models.ErrPipelineNotFound) {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get pipeline",
		})
	}

	// Validate that the resource definition exists. Only a missing definition
	// is a 404; any other read failure is a server error.
	if _, err := h.ResourceDefinitionModel.FindOne(pipeline.Resource); err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": fmt.Sprintf("Resource definition for type %s not found", pipeline.Resource),
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to find resource definition: %v", err),
		})
	}

	if err := h.Model.CreatePipeline(&pipeline); err != nil {
		if errors.Is(err, models.ErrPipelineExists) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": fmt.Sprintf("Pipeline %s already exists", pipeline.Name),
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create pipeline",
		})
	}
	return c.Status(fiber.StatusCreated).JSON(pipeline)
}

// GetPipeline retrieves a pipeline by name
// @Summary Get a pipeline by name
// @Description Retrieve a pipeline by its name
// @Tags pipelines
// @Accept json
// @Produce json
// @Param name path string true "Pipeline name"
// @Success 200 {object} types.Pipeline "Pipeline retrieved successfully"
// @Failure 404 {object} map[string]interface{} "Not found - Pipeline does not exist"
// @Failure 500 {object} map[string]interface{} "Internal server error"
// @Router /pipelines/{name} [get]
func (h *PipelineHandler) GetPipeline(c *fiber.Ctx) error {
	name := c.Params("name")
	pipeline, err := h.Model.GetPipeline(name)
	if err != nil {
		if errors.Is(err, models.ErrPipelineNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Pipeline not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get pipeline",
		})
	}
	return c.Status(fiber.StatusOK).JSON(pipeline)
}

// UpdatePipeline updates an existing pipeline
// @Summary Update an existing pipeline
// @Description Update an existing pipeline with new configuration. The resource type is immutable while resources reference the pipeline.
// @Tags pipelines
// @Accept json
// @Produce json
// @Param name path string true "Pipeline name"
// @Param pipeline body types.Pipeline true "Updated pipeline object"
// @Success 200 {object} types.Pipeline "Pipeline updated successfully"
// @Failure 400 {object} map[string]interface{} "Bad request - Invalid payload or definition"
// @Failure 404 {object} map[string]interface{} "Not found - Pipeline or resource definition does not exist"
// @Failure 409 {object} map[string]interface{} "Resource type cannot change while resources reference the pipeline"
// @Failure 500 {object} map[string]interface{} "Internal server error"
// @Router /pipelines/{name} [put]
func (h *PipelineHandler) UpdatePipeline(c *fiber.Ctx) error {
	name := c.Params("name")
	var pipeline types.Pipeline
	if err := c.BodyParser(&pipeline); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request payload",
		})
	}
	if name != pipeline.Name {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Pipeline name in URL and body do not match",
		})
	}
	if issues := utils.ValidatePipelineDefinition(pipeline); len(issues) > 0 {
		return issueResponse(c, fiber.StatusBadRequest, issues)
	}

	existing, err := h.Model.GetPipeline(name)
	if err != nil {
		if errors.Is(err, models.ErrPipelineNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Pipeline not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get pipeline",
		})
	}

	// A pipeline's resource type decides which resource index holds its runs.
	// Changing it would strand every resource created under the old type, so
	// it is blocked while any resource references the pipeline. With no
	// references the change is safe and allowed.
	referencing, count, err := h.conflictingResources(existing.Resource, name)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to list resources: %v", err),
		})
	}
	if pipeline.Resource != existing.Resource && count > 0 {
		return conflictResponse(c, fmt.Sprintf(
			"Pipeline %s resource type cannot change from %s to %s while resources reference it",
			name, existing.Resource, pipeline.Resource), referencing)
	}

	// The resource definition may have been deleted after the pipeline was
	// created; re-check it so an update cannot point at nothing.
	if _, err := h.ResourceDefinitionModel.FindOne(pipeline.Resource); err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": fmt.Sprintf("Resource definition for type %s not found", pipeline.Resource),
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to find resource definition: %v", err),
		})
	}

	if err := h.Model.UpdatePipeline(&pipeline); err != nil {
		if errors.Is(err, models.ErrPipelineNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Pipeline not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update pipeline",
		})
	}

	response := updatedPipelineResponse{Pipeline: pipeline}
	// Step order or IDs are allowed to change, but in-flight runs key results
	// on them. Surface how many resources could be affected instead of
	// blocking the update.
	if !reflect.DeepEqual(existing.Steps, pipeline.Steps) {
		response.AffectedResources = &count
		if count > 0 {
			response.Warning = fmt.Sprintf("Steps changed while %d resource(s) use this pipeline; runs in progress may not match their next step", count)
		}
	}
	return c.Status(fiber.StatusOK).JSON(response)
}

// updatedPipelineResponse keeps the pipeline fields flat for existing clients
// and adds update hints only when they apply.
type updatedPipelineResponse struct {
	types.Pipeline
	AffectedResources *int   `json:"affected_resources,omitempty"`
	Warning           string `json:"warning,omitempty"`
}

// DeletePipeline deletes a pipeline by name
// @Summary Delete a pipeline by name
// @Description Delete a pipeline by its name
// @Tags pipelines
// @Accept json
// @Produce json
// @Param name path string true "Pipeline name"
// @Success 204 "Pipeline deleted successfully"
// @Failure 404 {object} map[string]interface{} "Not found - Pipeline does not exist"
// @Failure 409 {object} map[string]interface{} "Resources still reference this pipeline"
// @Failure 500 {object} map[string]interface{} "Internal server error"
// @Router /pipelines/{name} [delete]
func (h *PipelineHandler) DeletePipeline(c *fiber.Ctx) error {
	name := c.Params("name")

	pipeline, err := h.Model.GetPipeline(name)
	if err != nil {
		if errors.Is(err, models.ErrPipelineNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Pipeline not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get pipeline",
		})
	}

	referencing, count, err := h.conflictingResources(pipeline.Resource, name)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Failed to list resources: %v", err),
		})
	}
	if count > 0 {
		return conflictResponse(c, fmt.Sprintf(
			"Pipeline %s is still referenced by %d resource(s)", name, count), referencing)
	}

	if err := h.Model.DeletePipeline(name); err != nil {
		if errors.Is(err, models.ErrPipelineNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "Pipeline not found",
			})
		}
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to delete pipeline",
		})
	}
	return c.SendStatus(fiber.StatusNoContent)
}

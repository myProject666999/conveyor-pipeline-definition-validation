package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/gofiber/fiber/v2"
	"github.com/open-ug/conveyor/internal/handlers"
	"github.com/open-ug/conveyor/internal/models"
	"github.com/open-ug/conveyor/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPipelineTestApp wires the pipeline handler onto an in-memory BadgerDB so
// the API can be exercised with app.Test without starting NATS or a server.
func newPipelineTestApp(t *testing.T) (*fiber.App, *badger.DB) {
	t.Helper()
	db, err := badger.Open(badger.DefaultOptions("").WithInMemory(true).WithLogger(nil))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	h := handlers.NewPipelineHandler(nil, db)
	app := fiber.New()
	app.Post("/pipelines", h.CreatePipeline)
	app.Get("/pipelines/:name", h.GetPipeline)
	app.Put("/pipelines/:name", h.UpdatePipeline)
	app.Delete("/pipelines/:name", h.DeletePipeline)
	return app, db
}

func createResourceDefinition(t *testing.T, db *badger.DB, name string) {
	t.Helper()
	require.NoError(t, models.NewResourceDefinitionModel(db).Insert(name, &types.ResourceDefinition{
		Name:   name,
		Schema: map[string]interface{}{"type": "object"},
	}))
}

func doJSON(t *testing.T, app *fiber.App, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, data
}

func decodeMap(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

func validPipeline(name, resource string) types.Pipeline {
	return types.Pipeline{
		Name:     name,
		Resource: resource,
		Steps: []types.Step{
			{ID: "1", Name: "Build", Driver: "buildpacks"},
			{ID: "2", Name: "Status", Driver: "status"},
			{ID: "3", Name: "Deploy", Driver: "docker"},
			{ID: "4", Name: "Status", Driver: "status"},
		},
	}
}

func TestCreatePipeline_Valid(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")

	resp, data := doJSON(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop"))
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var got types.Pipeline
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "gitops", got.Name)
	assert.Len(t, got.Steps, 4)
}

func TestCreatePipeline_DuplicateNameReturns409(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	// A different body under the same name must not overwrite the original.
	changed := validPipeline("gitops", "gitop")
	changed.Description = "second"
	resp, _ := doJSON(t, app, http.MethodPost, "/pipelines", changed)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	_, data := doJSON(t, app, http.MethodGet, "/pipelines/gitops", nil)
	var stored types.Pipeline
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Empty(t, stored.Description, "original pipeline was overwritten")
}

func TestCreatePipeline_DuplicateNameWinsOverMissingDefinition(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	// Same name, unknown resource type: the duplicate name is the reported
	// conflict rather than a 404 about the resource definition.
	resp, data := doJSON(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "absent"))
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, decodeMap(t, data)["error"], "already exists")
}

func TestCreatePipeline_MissingResourceDefinitionReturns404(t *testing.T) {
	app, _ := newPipelineTestApp(t)
	resp, _ := doJSON(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "absent"))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestCreatePipeline_EmptyStepsRejected(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")

	pipeline := validPipeline("gitops", "gitop")
	pipeline.Steps = nil
	resp, data := doJSON(t, app, http.MethodPost, "/pipelines", pipeline)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	m := decodeMap(t, data)
	assert.Contains(t, m["error"], "steps")
	errors, ok := m["errors"].([]interface{})
	require.True(t, ok, "expected errors array")
	assert.Len(t, errors, 1)
}

func TestCreatePipeline_DuplicateStepIDsRejected(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")

	pipeline := validPipeline("gitops", "gitop")
	pipeline.Steps[2].ID = "1"
	resp, data := doJSON(t, app, http.MethodPost, "/pipelines", pipeline)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	m := decodeMap(t, data)
	errs := m["errors"].([]interface{})
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "steps[2].id")
}

func TestCreatePipeline_DottedDriverRejected(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")

	pipeline := validPipeline("gitops", "gitop")
	pipeline.Steps[0].Driver = "drivers.status"
	resp, data := doJSON(t, app, http.MethodPost, "/pipelines", pipeline)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	errs := decodeMap(t, data)["errors"].([]interface{})
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0], "steps[0].driver")
}

func TestCreatePipeline_RepeatedDriverAllowed(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")

	pipeline := validPipeline("gitops", "gitop")
	// fixtures already repeat "status"; ensure that stays accepted.
	assert.Equal(t, "status", pipeline.Steps[1].Driver)
	assert.Equal(t, "status", pipeline.Steps[3].Driver)
	resp, _ := doJSON(t, app, http.MethodPost, "/pipelines", pipeline)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestUpdatePipeline_MissingReturns404(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")

	resp, _ := doJSON(t, app, http.MethodPut, "/pipelines/gitops", validPipeline("gitops", "gitop"))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestUpdatePipeline_ValidatesDefinition(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	invalid := validPipeline("gitops", "gitop")
	invalid.Steps = nil
	resp, _ := doJSON(t, app, http.MethodPut, "/pipelines/gitops", invalid)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestUpdatePipeline_ResourceChangeBlockedWhileReferenced(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	createResourceDefinition(t, db, "other")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	insertResource(t, db, "gitop", "app-1", "gitops")

	changed := validPipeline("gitops", "other")
	resp, data := doJSON(t, app, http.MethodPut, "/pipelines/gitops", changed)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	m := decodeMap(t, data)
	assert.Equal(t, float64(1), m["resource_count"])
	assert.Equal(t, []interface{}{"app-1"}, m["resources"])

	// The resource type must be untouched after the rejected update.
	_, got := doJSON(t, app, http.MethodGet, "/pipelines/gitops", nil)
	var stored types.Pipeline
	require.NoError(t, json.Unmarshal(got, &stored))
	assert.Equal(t, "gitop", stored.Resource)
}

func TestUpdatePipeline_ResourceChangeAllowedWithoutReferences(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	createResourceDefinition(t, db, "other")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	resp, _ := doJSON(t, app, http.MethodPut, "/pipelines/gitops", validPipeline("gitops", "other"))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestUpdatePipeline_StepChangeReportsAffectedResources(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))
	insertResource(t, db, "gitop", "app-1", "gitops")
	insertResource(t, db, "gitop", "app-2", "gitops")
	insertResource(t, db, "gitop", "unrelated", "")

	updated := validPipeline("gitops", "gitop")
	updated.Steps = append(updated.Steps, types.Step{ID: "5", Name: "Notify", Driver: "slack"})
	resp, data := doJSON(t, app, http.MethodPut, "/pipelines/gitops", updated)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	m := decodeMap(t, data)
	assert.Equal(t, float64(2), m["affected_resources"])
	assert.Contains(t, m["warning"], "2 resource(s)")
}

func TestUpdatePipeline_NoStepChangeOmitsHint(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	updated := validPipeline("gitops", "gitop")
	updated.Description = "only metadata"
	resp, data := doJSON(t, app, http.MethodPut, "/pipelines/gitops", updated)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	m := decodeMap(t, data)
	assert.NotContains(t, m, "warning")
	assert.NotContains(t, m, "affected_resources")
}

func TestDeletePipeline_ReferencedReturns409(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))
	insertResource(t, db, "gitop", "app-1", "gitops")

	resp, data := doJSON(t, app, http.MethodDelete, "/pipelines/gitops", nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, []interface{}{"app-1"}, decodeMap(t, data)["resources"])
}

func TestDeletePipeline_UnreferencedReturns204(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	resp, _ := doJSON(t, app, http.MethodDelete, "/pipelines/gitops", nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp, _ = doJSON(t, app, http.MethodGet, "/pipelines/gitops", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeletePipeline_MissingReturns404(t *testing.T) {
	app, _ := newPipelineTestApp(t)
	resp, _ := doJSON(t, app, http.MethodDelete, "/pipelines/gitops", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeletePipeline_ReportsAtMostTenReferences(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))
	for i := 0; i < 12; i++ {
		insertResource(t, db, "gitop", fmt.Sprintf("app-%02d", i), "gitops")
	}

	resp, data := doJSON(t, app, http.MethodDelete, "/pipelines/gitops", nil)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	m := decodeMap(t, data)
	assert.Equal(t, float64(12), m["resource_count"])
	assert.Len(t, m["resources"].([]interface{}), 10)
}

// A pipeline saved before validation existed stays readable and deletable; it
// only has to pass validation when written again.
func TestLegacyPipeline_ReadableAndDeletableButUpdateValidates(t *testing.T) {
	app, db := newPipelineTestApp(t)
	// Insert directly, bypassing the handler and its validation.
	legacy := &types.Pipeline{
		Name:     "Legacy_Pipeline",
		Resource: "gitop",
		Steps:    []types.Step{{ID: "1", Driver: "drivers.status"}},
	}
	require.NoError(t, models.NewPipelineModel(db).CreatePipeline(legacy))

	resp, data := doJSON(t, app, http.MethodGet, "/pipelines/Legacy_Pipeline", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var got types.Pipeline
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "Legacy_Pipeline", got.Name)

	// Writing it again must pass the new rules (name charset, dotted driver).
	resp, data = doJSON(t, app, http.MethodPut, "/pipelines/Legacy_Pipeline", legacy)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Len(t, decodeMap(t, data)["errors"].([]interface{}), 2)

	resp, _ = doJSON(t, app, http.MethodDelete, "/pipelines/Legacy_Pipeline", nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestUpdatePipeline_DeletedResourceDefinitionReturns404(t *testing.T) {
	app, db := newPipelineTestApp(t)
	createResourceDefinition(t, db, "gitop")
	require.Equal(t, http.StatusCreated, mustStatus(t, app, http.MethodPost, "/pipelines", validPipeline("gitops", "gitop")))

	require.NoError(t, models.NewResourceDefinitionModel(db).Delete("gitop"))

	resp, _ := doJSON(t, app, http.MethodPut, "/pipelines/gitops", validPipeline("gitops", "gitop"))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetPipeline_MissingReturns404(t *testing.T) {
	app, _ := newPipelineTestApp(t)
	resp, _ := doJSON(t, app, http.MethodGet, "/pipelines/gitops", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func mustStatus(t *testing.T, app *fiber.App, method, path string, body any) int {
	t.Helper()
	resp, _ := doJSON(t, app, method, path, body)
	return resp.StatusCode
}

// insertResource stores a resource so the reference scan can find it.
func insertResource(t *testing.T, db *badger.DB, resourceType, name, pipeline string) {
	t.Helper()
	data, err := json.Marshal(types.Resource{
		ID:       fmt.Sprintf("%s-%s", resourceType, name),
		Name:     name,
		Resource: resourceType,
		Pipeline: pipeline,
	})
	require.NoError(t, err)
	require.NoError(t, models.NewResourceModel(db).Insert(name, resourceType, data))
}

package models_test

import (
	"errors"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/open-ug/conveyor/internal/models"
	"github.com/open-ug/conveyor/pkg/types"
)

func TestPipelineModel_DoesNotOverwriteOrCreateImplicitly(t *testing.T) {
	db := setupTestDB(t)
	pm := models.NewPipelineModel(db)

	original := &types.Pipeline{Name: "gitops", Resource: "gitop"}
	if err := pm.CreatePipeline(original); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("create duplicate reports conflict", func(t *testing.T) {
		duplicate := &types.Pipeline{Name: "gitops", Resource: "other"}
		if err := pm.CreatePipeline(duplicate); !errors.Is(err, models.ErrPipelineExists) {
			t.Fatalf("expected ErrPipelineExists, got %v", err)
		}
		stored, err := pm.GetPipeline("gitops")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if stored.Resource != "gitop" {
			t.Fatalf("duplicate create overwrote the pipeline: %+v", stored)
		}
	})

	t.Run("update missing reports not found", func(t *testing.T) {
		if err := pm.UpdatePipeline(&types.Pipeline{Name: "absent"}); !errors.Is(err, models.ErrPipelineNotFound) {
			t.Fatalf("expected ErrPipelineNotFound, got %v", err)
		}
		if _, err := pm.GetPipeline("absent"); !errors.Is(err, models.ErrPipelineNotFound) {
			t.Fatalf("update created a missing pipeline: %v", err)
		}
	})

	t.Run("delete missing reports not found", func(t *testing.T) {
		if err := pm.DeletePipeline("absent"); !errors.Is(err, models.ErrPipelineNotFound) {
			t.Fatalf("expected ErrPipelineNotFound, got %v", err)
		}
	})

	t.Run("not found wraps badger sentinel", func(t *testing.T) {
		if _, err := pm.GetPipeline("absent"); !errors.Is(err, badger.ErrKeyNotFound) {
			t.Fatalf("expected wrapped badger.ErrKeyNotFound, got %v", err)
		}
	})
}

package models

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dgraph-io/badger/v4"
	"github.com/open-ug/conveyor/pkg/types"
)

var (
	// ErrPipelineExists is returned when a pipeline is created under a name
	// that is already in use. Pipelines are never silently overwritten.
	ErrPipelineExists = errors.New("pipeline already exists")
	// ErrPipelineNotFound is returned for reads, updates and deletes of a
	// pipeline that does not exist. It wraps badger.ErrKeyNotFound so callers
	// can use either sentinel with errors.Is.
	ErrPipelineNotFound = fmt.Errorf("pipeline not found: %w", badger.ErrKeyNotFound)
)

type PipelineModel struct {
	DB *badger.DB
}

func NewPipelineModel(db *badger.DB) *PipelineModel {
	return &PipelineModel{
		DB: db,
	}
}

// CreatePipeline stores a new pipeline. It fails with ErrPipelineExists when
// the name is already taken, so a create can never overwrite an existing
// definition (or silently replace a malformed legacy one).
func (pm *PipelineModel) CreatePipeline(pipeline *types.Pipeline) error {
	pipelineKey := fmt.Sprintf("/pipelines/%s", pipeline.Name)

	return pm.DB.Update(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(pipelineKey))
		if err == nil {
			return ErrPipelineExists
		}
		if !errors.Is(err, badger.ErrKeyNotFound) {
			return err
		}

		pipelineValue, err := json.Marshal(pipeline)
		if err != nil {
			return err
		}

		return txn.Set([]byte(pipelineKey), pipelineValue)
	})
}

func (pm *PipelineModel) GetPipeline(name string) (*types.Pipeline, error) {
	var pipeline *types.Pipeline

	err := pm.DB.View(func(txn *badger.Txn) error {
		pipelineKey := fmt.Sprintf("/pipelines/%s", name)
		item, err := txn.Get([]byte(pipelineKey))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrPipelineNotFound
			}
			return err
		}

		err = item.Value(func(val []byte) error {
			return json.Unmarshal(val, &pipeline)
		})
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return pipeline, nil
}

// UpdatePipeline replaces an existing pipeline. It fails with
// ErrPipelineNotFound rather than creating a missing one, so a PUT cannot
// implicitly create a definition that skipped validation.
func (pm *PipelineModel) UpdatePipeline(pipeline *types.Pipeline) error {
	pipelineKey := fmt.Sprintf("/pipelines/%s", pipeline.Name)

	return pm.DB.Update(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(pipelineKey))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrPipelineNotFound
			}
			return err
		}

		pipelineValue, err := json.Marshal(pipeline)
		if err != nil {
			return err
		}

		return txn.Set([]byte(pipelineKey), pipelineValue)
	})
}

// DeletePipeline removes a pipeline. It fails with ErrPipelineNotFound when
// the pipeline does not exist, so callers can distinguish a missing pipeline
// from a storage failure.
func (pm *PipelineModel) DeletePipeline(name string) error {
	pipelineKey := fmt.Sprintf("/pipelines/%s", name)

	return pm.DB.Update(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(pipelineKey))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return ErrPipelineNotFound
			}
			return err
		}

		return txn.Delete([]byte(pipelineKey))
	})
}

func (pm *PipelineModel) ListPipelines() ([]*types.Pipeline, error) {
	var pipelines []*types.Pipeline

	err := pm.DB.View(func(txn *badger.Txn) error {
		pipelinePrefix := "/pipelines/"
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Seek([]byte(pipelinePrefix)); it.ValidForPrefix([]byte(pipelinePrefix)); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var pipeline types.Pipeline
				err := json.Unmarshal(val, &pipeline)
				if err != nil {
					return err
				}
				pipelines = append(pipelines, &pipeline)
				return nil
			})
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return pipelines, nil
}

package blueprint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bgovanlu/vnprox/internal/change"
	"github.com/bgovanlu/vnprox/internal/inventory"
	"github.com/bgovanlu/vnprox/internal/store"
)

// InventorySource is the seam Service uses for a read snapshot to diff
// against and to capture from — mirrors internal/change.InventorySource's
// identical one-method seam over *inventory.Graph.
type InventorySource interface {
	Snapshot() inventory.Snapshot
}

// Config configures a Service.
type Config struct {
	Repo      *store.BlueprintRepo
	Inventory InventorySource
	Now       func() time.Time
}

// Service implements the blueprint list/get/save/delete/capture/
// instantiate operations internal/api's blueprints.go route handlers use
// (docs/api.md's Blueprints section: "GET/POST /blueprints",
// "POST /blueprints/{id}/instantiate"). It never applies or even creates a
// changeset itself — Instantiate returns the computed []change.Op plus a
// default title, and the caller (the API handler, mirroring
// internal/api/drift.go's handleDriftFix) hands them to
// change.Service.Create.
type Service struct {
	repo *store.BlueprintRepo
	inv  InventorySource
	now  func() time.Time
}

// New constructs a Service.
func New(cfg Config) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: cfg.Repo, inv: cfg.Inventory, now: now}
}

// List returns every blueprint: the five bundled starters first (in
// Starters' fixed order), then every saved blueprint ordered by name.
func (s *Service) List(ctx context.Context) ([]*Blueprint, error) {
	out := Starters()
	if s.repo == nil {
		return out, nil
	}
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("blueprint: listing: %w", err)
	}
	for _, row := range rows {
		bp, err := decodeRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, bp)
	}
	return out, nil
}

// Get returns one blueprint by id: a starter if id names one, otherwise a
// saved row, otherwise ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (*Blueprint, error) {
	if bp, ok := StarterByID(id); ok {
		return bp, nil
	}
	if s.repo == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("blueprint: getting %s: %w", id, err)
	}
	return decodeRow(row)
}

// Save validates and persists bp (docs/api.md: "POST /blueprints ...
// save"). An empty bp.ID mints a new ULID (new blueprint, e.g. authored
// from scratch or from Capture's output); a non-empty ID overwrites an
// existing saved blueprint with that id. Saving over a starter's id (or a
// blueprint whose ReadOnly is true) is rejected with ErrReadOnly — the
// documented "copy-to-edit" workflow is to save a new id derived from a
// starter's content, never to overwrite the starter itself.
func (s *Service) Save(ctx context.Context, author string, bp *Blueprint) (*Blueprint, error) {
	if bp.ReadOnly {
		return nil, fmt.Errorf("%w: cannot save a blueprint marked read-only", ErrReadOnly)
	}
	if _, ok := StarterByID(bp.ID); ok {
		return nil, fmt.Errorf("%w: %s is a bundled starter id", ErrReadOnly, bp.ID)
	}
	bp.BlueprintVersion = CurrentBlueprintVersion
	if bp.ID == "" {
		bp.ID = store.NewULID()
	}
	if err := Validate(bp); err != nil {
		return nil, err
	}
	if s.repo == nil {
		return nil, fmt.Errorf("blueprint: no store configured")
	}

	now := s.now().Unix()
	existing, err := s.repo.Get(ctx, bp.ID)
	createdAt := now
	if err == nil {
		createdAt = existing.CreatedAt
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("blueprint: saving %s: %w", bp.ID, err)
	}
	bp.CreatedAt = createdAt
	bp.UpdatedAt = now
	if bp.CreatedBy == "" {
		bp.CreatedBy = author
	}

	data, err := json.Marshal(bp)
	if err != nil {
		return nil, fmt.Errorf("blueprint: encoding %s: %w", bp.ID, err)
	}
	row := store.Blueprint{
		ID: bp.ID, Name: bp.Name, BlueprintJSON: string(data),
		CreatedBy: bp.CreatedBy, CreatedAt: bp.CreatedAt, UpdatedAt: bp.UpdatedAt,
	}
	if err := s.repo.Put(ctx, row); err != nil {
		return nil, fmt.Errorf("blueprint: saving %s: %w", bp.ID, err)
	}
	return bp, nil
}

// Delete removes a saved blueprint. Deleting a starter id is rejected with
// ErrReadOnly.
func (s *Service) Delete(ctx context.Context, id string) error {
	if _, ok := StarterByID(id); ok {
		return fmt.Errorf("%w: %s is a bundled starter id", ErrReadOnly, id)
	}
	if s.repo == nil {
		return fmt.Errorf("blueprint: no store configured")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("blueprint: deleting %s: %w", id, err)
	}
	return nil
}

// Capture builds an (unsaved) Blueprint from node's current live network
// state ("blueprint-ify", docs/features/blueprints.md §1) — the caller
// decides whether to hand the result to Save.
func (s *Service) Capture(node string) (*Blueprint, error) {
	if s.inv == nil {
		return nil, fmt.Errorf("blueprint: no inventory source configured")
	}
	return Capture(s.inv.Snapshot(), node)
}

// Instantiate resolves id (starter or saved), computes the idempotent op
// diff against the live inventory snapshot (Instantiate, package-level),
// and returns those ops plus a default changeset title
// ("blueprint: <name>") for the caller to hand to change.Service.Create —
// mirroring internal/drift.Service.FixOps's ops+title+ok return shape
// (internal/api/drift.go's handleDriftFix consumes it the same way).
func (s *Service) Instantiate(ctx context.Context, id string, req InstantiateRequest) (ops []change.Op, title string, err error) {
	bp, err := s.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if s.inv == nil {
		return nil, "", fmt.Errorf("blueprint: no inventory source configured")
	}
	ops, err = Instantiate(bp, req, s.inv.Snapshot())
	if err != nil {
		return nil, "", err
	}
	title = req.Title
	if title == "" {
		title = "blueprint: " + bp.Name
	}
	return ops, title, nil
}

// SuggestAddress resolves paramName's next-free address for blueprint id
// (T-603 AC4).
func (s *Service) SuggestAddress(ctx context.Context, id, paramName string) (string, error) {
	bp, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if s.inv == nil {
		return "", fmt.Errorf("blueprint: no inventory source configured")
	}
	return SuggestForParam(bp, paramName, s.inv.Snapshot())
}

func decodeRow(row store.Blueprint) (*Blueprint, error) {
	var bp Blueprint
	if err := json.Unmarshal([]byte(row.BlueprintJSON), &bp); err != nil {
		return nil, fmt.Errorf("blueprint: decoding stored blueprint %s: %w", row.ID, err)
	}
	return &bp, nil
}

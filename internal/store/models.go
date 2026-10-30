package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/routing"
)

// Model types and target strategies.
var (
	ModelTypes = []string{"chat", "embedding", "image", "video", "tts", "asr"}
	Strategies = []string{routing.Priority, routing.RoundRobin, routing.Weighted, routing.Random}
)

// Model is a public model name and how it is routed.
type Model struct {
	ID                    int64     `json:"id"`
	Name                  string    `json:"name"`
	Type                  string    `json:"type"`
	Strategy              string    `json:"strategy"`
	MaxTotalAttempts      int       `json:"max_total_attempts"`
	MaxKeysPerTarget      int       `json:"max_keys_per_target"`
	RetryOn               []string  `json:"retry_on"`
	AttemptTimeoutSeconds int       `json:"attempt_timeout_seconds"`
	CreatedAt             time.Time `json:"created_at"`
	Targets               []Target  `json:"targets"` // in priority order
}

// Target is one (provider, upstream model) candidate of a model.
type Target struct {
	ID              int64  `json:"id"`
	ProviderID      int64  `json:"provider_id"`
	ProviderName    string `json:"provider_name"`
	ProviderType    string `json:"provider_type"`
	ProviderEnabled bool   `json:"provider_enabled"`
	UpstreamModel   string `json:"upstream_model"`
	Weight          int    `json:"weight"`
	Enabled         bool   `json:"enabled"`
}

// ModelInput creates or edits a model; Targets replace the existing ones.
type ModelInput struct {
	Name                  string        `json:"name"`
	Type                  string        `json:"type"`
	Strategy              string        `json:"strategy"`
	MaxTotalAttempts      int           `json:"max_total_attempts"`
	MaxKeysPerTarget      int           `json:"max_keys_per_target"`
	RetryOn               []string      `json:"retry_on"`
	AttemptTimeoutSeconds int           `json:"attempt_timeout_seconds"`
	Targets               []TargetInput `json:"targets"`
}

// TargetInput is one target of a ModelInput.
type TargetInput struct {
	ProviderID    int64  `json:"provider_id"`
	UpstreamModel string `json:"upstream_model"`
	Weight        int    `json:"weight"`
	Enabled       bool   `json:"enabled"`
}

func (s *Store) normalizeModel(ctx context.Context, in *ModelInput) error {
	errs := FieldErrors{}
	in.Name = strings.TrimSpace(in.Name)
	switch {
	case in.Name == "":
		errs["name"] = "Name is required"
	case strings.ContainsAny(in.Name, " \t\r\n"):
		errs["name"] = "Name must not contain spaces"
	case len(in.Name) > 200:
		errs["name"] = "Name is too long"
	}
	if !contains(ModelTypes, in.Type) {
		errs["type"] = "Choose a model type"
	}
	if in.Strategy == "" {
		in.Strategy = routing.Priority
	}
	if !contains(Strategies, in.Strategy) {
		errs["strategy"] = "Choose priority, round_robin, weighted or random"
	}
	if in.MaxTotalAttempts < 1 || in.MaxTotalAttempts > 20 {
		errs["max_total_attempts"] = "Must be between 1 and 20"
	}
	if in.MaxKeysPerTarget < 1 || in.MaxKeysPerTarget > 20 {
		errs["max_keys_per_target"] = "Must be between 1 and 20"
	}
	if in.AttemptTimeoutSeconds < 1 || in.AttemptTimeoutSeconds > 3600 {
		errs["attempt_timeout_seconds"] = "Must be between 1 and 3600 seconds"
	}
	retry := []string{}
	for _, v := range routing.RetryOnValues { // canonical order, no duplicates
		if contains(in.RetryOn, v) {
			retry = append(retry, v)
		}
	}
	for _, v := range in.RetryOn {
		if !contains(routing.RetryOnValues, v) {
			errs["retry_on"] = fmt.Sprintf("Unknown value %q", v)
		}
	}
	in.RetryOn = retry
	if len(in.Targets) == 0 {
		errs["targets"] = "Add at least one target"
	}
	types, err := s.providerTypes(ctx)
	if err != nil {
		return err
	}
	for i := range in.Targets {
		t := &in.Targets[i]
		prefix := fmt.Sprintf("targets.%d.", i)
		t.UpstreamModel = strings.TrimSpace(t.UpstreamModel)
		ptype, ok := types[t.ProviderID]
		switch {
		case !ok:
			errs[prefix+"provider_id"] = "Choose a provider"
		case in.Type != "chat" && ptype != "openai":
			errs[prefix+"provider_id"] = "Non-chat models can only use OpenAI-compatible providers"
		}
		if t.UpstreamModel == "" {
			errs[prefix+"upstream_model"] = "Upstream model is required"
		}
		if t.Weight == 0 {
			t.Weight = 1
		}
		if t.Weight < 1 || t.Weight > 1000 {
			errs[prefix+"weight"] = "Weight must be between 1 and 1000"
		}
	}
	return errs.orNil()
}

func (s *Store) providerTypes(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, type FROM providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var (
			id int64
			t  string
		)
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

const modelSelect = `SELECT id, name, type, strategy, max_total_attempts, max_keys_per_target, retry_on,
	attempt_timeout_seconds, created_at FROM models`

func (s *Store) queryModels(ctx context.Context, where string, args ...any) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, modelSelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	var models []Model
	for rows.Next() {
		var (
			m       Model
			retry   string
			created int64
		)
		if err := rows.Scan(&m.ID, &m.Name, &m.Type, &m.Strategy, &m.MaxTotalAttempts, &m.MaxKeysPerTarget,
			&retry, &m.AttemptTimeoutSeconds, &created); err != nil {
			rows.Close()
			return nil, err
		}
		m.RetryOn = []string{}
		if retry != "" {
			m.RetryOn = strings.Split(retry, ",")
		}
		m.CreatedAt = fromMS(created)
		m.Targets = []Target{}
		models = append(models, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return models, nil
	}
	index := map[int64]int{}
	for i, m := range models {
		index[m.ID] = i
	}
	trows, err := s.db.QueryContext(ctx, `SELECT t.id, t.model_id, t.provider_id, p.name, p.type, p.enabled,
		t.upstream_model, t.weight, t.enabled
		FROM model_targets t JOIN providers p ON p.id = t.provider_id ORDER BY t.model_id, t.position, t.id`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var (
			t                Target
			modelID          int64
			pEnabled, enable int
		)
		if err := trows.Scan(&t.ID, &modelID, &t.ProviderID, &t.ProviderName, &t.ProviderType, &pEnabled,
			&t.UpstreamModel, &t.Weight, &enable); err != nil {
			return nil, err
		}
		t.ProviderEnabled, t.Enabled = pEnabled == 1, enable == 1
		if i, ok := index[modelID]; ok {
			models[i].Targets = append(models[i].Targets, t)
		}
	}
	return models, trows.Err()
}

// ListModels returns all models sorted by name.
func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	models, err := s.queryModels(ctx, `ORDER BY name COLLATE NOCASE`)
	if models == nil {
		models = []Model{}
	}
	return models, err
}

// GetModel returns one model by id.
func (s *Store) GetModel(ctx context.Context, id int64) (Model, error) {
	return s.oneModel(ctx, `WHERE id = ?`, id)
}

// ModelByName returns one model by its public name.
func (s *Store) ModelByName(ctx context.Context, name string) (Model, error) {
	return s.oneModel(ctx, `WHERE name = ?`, name)
}

func (s *Store) oneModel(ctx context.Context, where string, arg any) (Model, error) {
	models, err := s.queryModels(ctx, where, arg)
	if err != nil {
		return Model{}, err
	}
	if len(models) == 0 {
		return Model{}, ErrNotFound
	}
	return models[0], nil
}

// CreateModel validates and inserts a model with its targets.
func (s *Store) CreateModel(ctx context.Context, in ModelInput) (Model, error) {
	if err := s.normalizeModel(ctx, &in); err != nil {
		return Model{}, err
	}
	var id int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO models (name, type, strategy, max_total_attempts, max_keys_per_target,
			retry_on, attempt_timeout_seconds, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Name, in.Type, in.Strategy, in.MaxTotalAttempts, in.MaxKeysPerTarget, strings.Join(in.RetryOn, ","),
			in.AttemptTimeoutSeconds, ms(s.now()))
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return insertTargets(ctx, tx, id, in.Targets)
	})
	if isUnique(err) {
		return Model{}, FieldErrors{"name": "A model with this name already exists"}
	}
	if err != nil {
		return Model{}, err
	}
	return s.GetModel(ctx, id)
}

// UpdateModel validates and updates a model, replacing its targets.
func (s *Store) UpdateModel(ctx context.Context, id int64, in ModelInput) (Model, error) {
	if err := s.normalizeModel(ctx, &in); err != nil {
		return Model{}, err
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE models SET name = ?, type = ?, strategy = ?, max_total_attempts = ?,
			max_keys_per_target = ?, retry_on = ?, attempt_timeout_seconds = ? WHERE id = ?`,
			in.Name, in.Type, in.Strategy, in.MaxTotalAttempts, in.MaxKeysPerTarget, strings.Join(in.RetryOn, ","),
			in.AttemptTimeoutSeconds, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM model_targets WHERE model_id = ?`, id); err != nil {
			return err
		}
		return insertTargets(ctx, tx, id, in.Targets)
	})
	if isUnique(err) {
		return Model{}, FieldErrors{"name": "A model with this name already exists"}
	}
	if err != nil {
		return Model{}, err
	}
	return s.GetModel(ctx, id)
}

func insertTargets(ctx context.Context, tx *sql.Tx, modelID int64, targets []TargetInput) error {
	for i, t := range targets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO model_targets (model_id, position, provider_id, upstream_model, weight, enabled)
			VALUES (?, ?, ?, ?, ?, ?)`, modelID, i, t.ProviderID, t.UpstreamModel, t.Weight, boolInt(t.Enabled)); err != nil {
			return err
		}
	}
	return nil
}

// DeleteModel deletes a model.
func (s *Store) DeleteModel(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM models WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

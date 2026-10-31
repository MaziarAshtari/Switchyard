package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/routing"
)

// LogEntry is one request log row (list view).
type LogEntry struct {
	ID            int64     `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Endpoint      string    `json:"endpoint"`
	Model         string    `json:"model"`
	ModelType     string    `json:"model_type"`
	ProviderID    *int64    `json:"-"`
	ProviderName  string    `json:"provider_name"`
	KeyID         *int64    `json:"-"`
	KeyHint       string    `json:"key_hint"`
	UpstreamModel string    `json:"upstream_model"`
	StatusCode    int       `json:"status_code"`
	LatencyMS     int64     `json:"latency_ms"`
	Stream        bool      `json:"stream"`
	InputTokens   *int64    `json:"input_tokens"`
	OutputTokens  *int64    `json:"output_tokens"`
	Attempts      int       `json:"attempts"`
	Error         string    `json:"error"`
}

// LogDetail is a full request log row.
type LogDetail struct {
	LogEntry
	AttemptDetails []Attempt `json:"attempt_details"`
	RequestBody    *string   `json:"request_body"`
	ResponseBody   *string   `json:"response_body"`
}

// Attempt describes one upstream attempt of a request.
type Attempt struct {
	ProviderName  string `json:"provider_name"`
	KeyHint       string `json:"key_hint"`
	UpstreamModel string `json:"upstream_model"`
	StatusCode    int    `json:"status_code"`
	LatencyMS     int64  `json:"latency_ms"`
	Error         string `json:"error"`
	// VisionBridge marks a vision bridge sub-request rather than an upstream
	// attempt: the public model that described an image (the sub-request is
	// also logged on its own).
	VisionBridge string `json:"vision_bridge,omitempty"`
}

// LogFilter selects request logs. Zero values mean "any".
type LogFilter struct {
	Model      string
	ProviderID int64
	Status     string // "success" or "error"
	From, To   time.Time
	BeforeID   int64 // cursor: only rows with a smaller id
	Limit      int
}

// InsertLog stores a request log row.
func (s *Store) InsertLog(ctx context.Context, d *LogDetail) error {
	attempts, _ := json.Marshal(d.AttemptDetails)
	if d.AttemptDetails == nil {
		attempts = []byte("[]")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO request_logs (created_at, endpoint, model, model_type, provider_id,
		provider_name, key_id, key_hint, upstream_model, status_code, latency_ms, stream, input_tokens, output_tokens,
		attempts, error, attempt_details, request_body, response_body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ms(d.CreatedAt), d.Endpoint, d.Model, d.ModelType, d.ProviderID, d.ProviderName, d.KeyID, d.KeyHint,
		d.UpstreamModel, d.StatusCode, d.LatencyMS, boolInt(d.Stream), d.InputTokens, d.OutputTokens, d.Attempts,
		truncate(d.Error, 2000), string(attempts), d.RequestBody, d.ResponseBody)
	return err
}

const logColumns = `id, created_at, endpoint, model, model_type, provider_id, provider_name, key_id, key_hint,
	upstream_model, status_code, latency_ms, stream, input_tokens, output_tokens, attempts, error`

func scanLog(row interface{ Scan(...any) error }, extra ...any) (LogEntry, error) {
	var (
		e                  LogEntry
		created            int64
		stream             int
		providerID, keyID  sql.NullInt64
		inTokens, outToken sql.NullInt64
	)
	dest := []any{&e.ID, &created, &e.Endpoint, &e.Model, &e.ModelType, &providerID, &e.ProviderName, &keyID,
		&e.KeyHint, &e.UpstreamModel, &e.StatusCode, &e.LatencyMS, &stream, &inTokens, &outToken, &e.Attempts, &e.Error}
	if err := row.Scan(append(dest, extra...)...); err != nil {
		return e, err
	}
	e.CreatedAt, e.Stream = fromMS(created), stream == 1
	if providerID.Valid {
		e.ProviderID = &providerID.Int64
	}
	if keyID.Valid {
		e.KeyID = &keyID.Int64
	}
	if inTokens.Valid {
		e.InputTokens = &inTokens.Int64
	}
	if outToken.Valid {
		e.OutputTokens = &outToken.Int64
	}
	return e, nil
}

// ListLogs returns matching logs, newest first, and the cursor for the next page.
func (s *Store) ListLogs(ctx context.Context, f LogFilter) ([]LogEntry, *int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	var (
		where []string
		args  []any
	)
	if f.Model != "" {
		where, args = append(where, "model = ?"), append(args, f.Model)
	}
	if f.ProviderID != 0 {
		where, args = append(where, "provider_id = ?"), append(args, f.ProviderID)
	}
	switch f.Status {
	case "success":
		where = append(where, "status_code BETWEEN 200 AND 299")
	case "error":
		where = append(where, "status_code NOT BETWEEN 200 AND 299")
	}
	if !f.From.IsZero() {
		where, args = append(where, "created_at >= ?"), append(args, ms(f.From))
	}
	if !f.To.IsZero() {
		where, args = append(where, "created_at <= ?"), append(args, ms(f.To))
	}
	if f.BeforeID > 0 {
		where, args = append(where, "id < ?"), append(args, f.BeforeID)
	}
	query := "SELECT " + logColumns + " FROM request_logs"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, f.Limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		e, err := scanLog(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *int64
	if len(out) > f.Limit {
		out = out[:f.Limit]
		id := out[len(out)-1].ID
		next = &id
	}
	return out, next, nil
}

// GetLog returns one log row with attempt details and bodies.
func (s *Store) GetLog(ctx context.Context, id int64) (LogDetail, error) {
	var (
		d              LogDetail
		attempts       string
		reqBody, rBody sql.NullString
	)
	e, err := scanLog(s.db.QueryRowContext(ctx, "SELECT "+logColumns+
		", attempt_details, request_body, response_body FROM request_logs WHERE id = ?", id), &attempts, &reqBody, &rBody)
	if err != nil {
		return d, notFound(err)
	}
	d.LogEntry = e
	d.AttemptDetails = []Attempt{}
	_ = json.Unmarshal([]byte(attempts), &d.AttemptDetails)
	if reqBody.Valid {
		d.RequestBody = &reqBody.String
	}
	if rBody.Valid {
		d.ResponseBody = &rBody.String
	}
	return d, nil
}

// VideoJob maps an upstream video job to the key that created it.
type VideoJob struct {
	ID         string
	Model      string
	ProviderID int64
	KeyID      int64
}

// SaveVideoJob records which provider/key created a video job.
func (s *Store) SaveVideoJob(ctx context.Context, j VideoJob) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO video_jobs (id, model, provider_id, key_id, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET model = excluded.model, provider_id = excluded.provider_id, key_id = excluded.key_id`,
		j.ID, j.Model, j.ProviderID, j.KeyID, ms(s.now()))
	return err
}

// GetVideoJob looks up a video job.
func (s *Store) GetVideoJob(ctx context.Context, id string) (VideoJob, error) {
	j := VideoJob{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT model, provider_id, key_id FROM video_jobs WHERE id = ?`, id).
		Scan(&j.Model, &j.ProviderID, &j.KeyID)
	return j, notFound(err)
}

// Prune deletes request logs and video jobs older than the retention period
// and expired sessions. It returns the number of deleted log rows.
func (s *Store) Prune(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := ms(s.now().Add(-retention))
	res, err := s.db.ExecContext(ctx, `DELETE FROM request_logs WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM video_jobs WHERE created_at < ?`, cutoff); err != nil {
		return n, err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, ms(s.now()))
	return n, err
}

// Dashboard aggregates request statistics since a point in time.
type Dashboard struct {
	Requests      int         `json:"requests"`
	Errors        int         `json:"errors"`
	SuccessRate   float64     `json:"success_rate"`
	AvgLatencyMS  int64       `json:"avg_latency_ms"`
	InputTokens   int64       `json:"input_tokens"`
	OutputTokens  int64       `json:"output_tokens"`
	Hourly        []HourStat  `json:"hourly"`
	ByModel       []NameStat  `json:"by_model"`
	ByProvider    []NameStat  `json:"by_provider"`
	Keys          KeyCounts   `json:"keys"`
	UnhealthyKeys []APIKey    `json:"unhealthy_keys"`
	Setup         SetupCounts `json:"setup"`
}

// HourStat counts requests in the hour starting `hour` hours after `since`.
type HourStat struct {
	Hour     int `json:"hour"`
	Requests int `json:"requests"`
	Errors   int `json:"errors"`
}

// NameStat counts requests per model or provider.
type NameStat struct {
	Name     string `json:"name"`
	Requests int    `json:"requests"`
	Errors   int    `json:"errors"`
}

// SetupCounts tells the GUI which getting-started steps are done.
type SetupCounts struct {
	Providers   int `json:"providers"`
	Keys        int `json:"keys"`
	Models      int `json:"models"`
	GatewayKeys int `json:"gateway_keys"`
}

const isError = "(status_code NOT BETWEEN 200 AND 299)"

// Dashboard computes the dashboard statistics.
func (s *Store) Dashboard(ctx context.Context, since time.Time) (Dashboard, error) {
	d := Dashboard{Hourly: []HourStat{}, ByModel: []NameStat{}, ByProvider: []NameStat{}, UnhealthyKeys: []APIKey{}}
	from := ms(since)
	var avg sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(`+isError+`), 0), AVG(latency_ms),
		COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0) FROM request_logs WHERE created_at >= ?`, from).
		Scan(&d.Requests, &d.Errors, &avg, &d.InputTokens, &d.OutputTokens)
	if err != nil {
		return d, err
	}
	d.AvgLatencyMS = int64(avg.Float64)
	if d.Requests > 0 {
		d.SuccessRate = float64(d.Requests-d.Errors) / float64(d.Requests)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT (created_at - ?) / 3600000 AS h, COUNT(*), SUM(`+isError+`)
		FROM request_logs WHERE created_at >= ? GROUP BY h ORDER BY h`, from, from)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var h HourStat
		if err := rows.Scan(&h.Hour, &h.Requests, &h.Errors); err != nil {
			rows.Close()
			return d, err
		}
		d.Hourly = append(d.Hourly, h)
	}
	rows.Close()

	byName := func(column string) ([]NameStat, error) {
		rows, err := s.db.QueryContext(ctx, `SELECT `+column+`, COUNT(*), SUM(`+isError+`) FROM request_logs
			WHERE created_at >= ? AND `+column+` != '' GROUP BY `+column+` ORDER BY 2 DESC LIMIT 10`, from)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []NameStat{}
		for rows.Next() {
			var n NameStat
			if err := rows.Scan(&n.Name, &n.Requests, &n.Errors); err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, rows.Err()
	}
	if d.ByModel, err = byName("model"); err != nil {
		return d, err
	}
	if d.ByProvider, err = byName("provider_name"); err != nil {
		return d, err
	}

	keys, err := s.ListKeys(ctx, 0)
	if err != nil {
		return d, err
	}
	for _, k := range keys {
		d.Keys.add(k.Enabled, k.Status)
		if k.Enabled && (k.Status != routing.StatusActive || !k.ProxyOK()) && len(d.UnhealthyKeys) < 20 {
			d.UnhealthyKeys = append(d.UnhealthyKeys, k)
		}
	}
	err = s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM providers), (SELECT COUNT(*) FROM api_keys),
		(SELECT COUNT(*) FROM models), (SELECT COUNT(*) FROM gateway_keys)`).
		Scan(&d.Setup.Providers, &d.Setup.Keys, &d.Setup.Models, &d.Setup.GatewayKeys)
	return d, err
}

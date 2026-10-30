package gateway

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// LogWriter stores request logs from a single background goroutine so that
// requests never wait for the database.
type LogWriter struct {
	store *store.Store
	log   *slog.Logger
	ch    chan *store.LogDetail
	wg    sync.WaitGroup
}

// NewLogWriter starts the writer.
func NewLogWriter(st *store.Store, log *slog.Logger) *LogWriter {
	w := &LogWriter{store: st, log: log, ch: make(chan *store.LogDetail, 4096)}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for e := range w.ch {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := st.InsertLog(ctx, e); err != nil {
				log.Error("write request log", "err", err)
			}
			cancel()
		}
	}()
	return w
}

// Write queues an entry. If the queue is full the entry is dropped (and
// reported) rather than blocking the request.
func (w *LogWriter) Write(e *store.LogDetail) {
	select {
	case w.ch <- e:
	default:
		w.log.Warn("request log queue full, dropping entry", "model", e.Model)
	}
}

// Close flushes queued entries and stops the writer.
func (w *LogWriter) Close() {
	close(w.ch)
	w.wg.Wait()
}

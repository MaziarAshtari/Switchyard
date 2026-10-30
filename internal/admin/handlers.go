package admin

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/MaziarAshtari/Switchyard/internal/gateway"
	"github.com/MaziarAshtari/Switchyard/internal/proxy"
	"github.com/MaziarAshtari/Switchyard/internal/store"
)

// --- providers ---

func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListProviders(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if list == nil {
		list = []store.Provider{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) getProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	p, err := a.store.GetProvider(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) createProvider(w http.ResponseWriter, r *http.Request) {
	var in store.ProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.store.CreateProvider(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in store.ProviderInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.store.UpdateProvider(r.Context(), id, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteProvider(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- keys ---

func (a *API) listProviderKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	keys, err := a.store.ListKeys(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (a *API) listAllKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.store.ListKeys(r.Context(), 0)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in store.KeyInput
	if !decode(w, r, &in) {
		return
	}
	k, err := a.store.CreateKey(r.Context(), id, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (a *API) bulkAddKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := a.store.BulkAddKeys(r.Context(), id, in.Text)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) updateKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in store.KeyUpdate
	if !decode(w, r, &in) {
		return
	}
	k, err := a.store.UpdateKey(r.Context(), id, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (a *API) deleteKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteKey(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) resetKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	k, err := a.store.ResetKey(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (a *API) testKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	res, err := a.gw.TestKey(detached(r), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) batchKeys(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if !decode(w, r, &in) {
		return
	}
	n, err := a.store.BatchKeys(r.Context(), in.IDs, in.Action)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

// --- proxies ---

func (a *API) listProxies(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListProxies(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) createProxy(w http.ResponseWriter, r *http.Request) {
	var in store.ProxyInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.store.CreateProxy(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *API) updateProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in store.ProxyInput
	if !decode(w, r, &in) {
		return
	}
	p, err := a.store.UpdateProxy(r.Context(), id, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.clients.Reset()
	writeJSON(w, http.StatusOK, p)
}

func (a *API) deleteProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteProxy(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	a.clients.Reset()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) importProxies(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text        string `json:"text"`
		DefaultType string `json:"default_type"`
		DryRun      bool   `json:"dry_run"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := a.store.ImportProxies(r.Context(), in.Text, in.DefaultType, in.DryRun)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// proxyTestResult is a TestResult for one proxy.
type proxyTestResult struct {
	ProxyID int64 `json:"proxy_id"`
	proxy.TestResult
}

func (a *API) runProxyTest(r *http.Request, id int64, target string) (proxy.TestResult, error) {
	spec, _, err := a.store.ProxySpec(r.Context(), id)
	if err != nil {
		return proxy.TestResult{}, err
	}
	return proxy.Test(detached(r), a.clients.Get(spec.URL()), target), nil
}

func (a *API) testProxy(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	set, err := a.store.Settings(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	res, err := a.runProxyTest(r, id, set.ProxyTestURL)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// testProxies tests the given proxies (all when ids is empty) concurrently.
func (a *API) testProxies(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	set, err := a.store.Settings(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	if len(in.IDs) == 0 {
		list, err := a.store.ListProxies(r.Context())
		if err != nil {
			a.fail(w, err)
			return
		}
		for _, p := range list {
			in.IDs = append(in.IDs, p.ID)
		}
	}
	results := make([]proxyTestResult, len(in.IDs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, id := range in.IDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := a.runProxyTest(r, id, set.ProxyTestURL)
			if err != nil {
				res = proxy.TestResult{Error: err.Error()}
			}
			results[i] = proxyTestResult{ProxyID: id, TestResult: res}
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (a *API) assignProxies(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProxyIDs []int64 `json:"proxy_ids"`
		KeyIDs   []int64 `json:"key_ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	fields := store.FieldErrors{}
	if len(in.ProxyIDs) == 0 {
		fields["proxy_ids"] = "Select at least one proxy"
	}
	if len(in.KeyIDs) == 0 {
		fields["key_ids"] = "Select at least one key"
	}
	for _, id := range in.ProxyIDs {
		if _, err := a.store.GetProxy(r.Context(), id); err != nil {
			fields["proxy_ids"] = "Proxy " + strconv.FormatInt(id, 10) + " not found"
		}
	}
	if len(fields) > 0 {
		a.fail(w, fields)
		return
	}
	n, err := a.store.AssignProxies(r.Context(), proxy.Assign(in.KeyIDs, in.ProxyIDs))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

// --- models ---

func (a *API) listModels(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListModels(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) getModel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	m, err := a.store.GetModel(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (a *API) createModel(w http.ResponseWriter, r *http.Request) {
	var in store.ModelInput
	if !decode(w, r, &in) {
		return
	}
	m, err := a.store.CreateModel(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (a *API) updateModel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in store.ModelInput
	if !decode(w, r, &in) {
		return
	}
	m, err := a.store.UpdateModel(r.Context(), id, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (a *API) deleteModel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteModel(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) testModel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	m, err := a.store.GetModel(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	res, err := a.gw.TestModel(detached(r), m)
	if errors.Is(err, gateway.ErrNotTestable) {
		writeError(w, http.StatusBadRequest, "Video models cannot be tested: generation is slow and expensive.")
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- logs and dashboard ---

func parseTime(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, v)
}

func (a *API) listLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.LogFilter{Model: q.Get("model"), Status: q.Get("status")}
	var err error
	if f.From, err = parseTime(q.Get("from")); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid 'from' time")
		return
	}
	if f.To, err = parseTime(q.Get("to")); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid 'to' time")
		return
	}
	f.ProviderID, _ = strconv.ParseInt(q.Get("provider_id"), 10, 64)
	f.BeforeID, _ = strconv.ParseInt(q.Get("before_id"), 10, 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	items, next, err := a.store.ListLogs(r.Context(), f)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_before_id": next})
}

func (a *API) getLog(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	d, err := a.store.GetLog(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	since, err := parseTime(r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid 'since' time")
		return
	}
	if since.IsZero() {
		now := a.now().UTC()
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	d, err := a.store.Dashboard(r.Context(), since)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// --- settings and gateway keys ---

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := a.store.Settings(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) saveSettings(w http.ResponseWriter, r *http.Request) {
	var in store.Settings
	if !decode(w, r, &in) {
		return
	}
	s, err := a.store.SaveSettings(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) listGatewayKeys(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListGatewayKeys(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) createGatewayKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	k, err := a.store.CreateGatewayKey(r.Context(), in.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (a *API) updateGatewayKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var in store.GatewayKeyInput
	if !decode(w, r, &in) {
		return
	}
	k, err := a.store.UpdateGatewayKey(r.Context(), id, in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (a *API) deleteGatewayKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteGatewayKey(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

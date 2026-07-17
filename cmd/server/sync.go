package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// ---------------------------------------------------------------------------
// Full-sync endpoints + the persisted import-queue processor.
//
// Flow: connect → POST .../sync/preview (discover + summarize, no enqueue) →
// user picks skip-vs-redownload → POST .../sync/start (discover + enqueue) →
// the processor goroutine drains the queue paced + rate-limit-aware →
// result_router marks each item done after ingest.
// ---------------------------------------------------------------------------

type syncDiscoverItem struct {
	ItemType   string `json:"item_type"`
	ExternalID string `json:"external_id"`
	ItemTime   string `json:"item_time"`
}

type syncDiscoverResp struct {
	Items       []syncDiscoverItem `json:"items"`
	Complete    bool               `json:"complete"`
	NextPage    int                `json:"next_page,omitempty"`
	RateLimited bool               `json:"rate_limited,omitempty"`
	Error       string             `json:"error,omitempty"`
}

// fetchSourceJobBody mirrors the worker's fetch_source job input (JSON job
// bodies — distinct from the typed proto JobResult the worker returns).
type fetchSourceJobBody struct {
	JobID        string `json:"job_id"`
	AccountID    string `json:"account_id"`
	UserID       string `json:"user_id"`
	Provider     string `json:"provider"`
	ExtID        string `json:"ext_id"`
	FetchStreams bool   `json:"fetch_streams"`
	Reason       string `json:"reason"`
}

func mountSyncEndpoints(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.NATSBus == nil {
		logger.Info("sync endpoints not mounted: NATS not wired")
		return
	}

	queueCounts := func(ctx context.Context, id domain.ExternalAccountID) map[string]int {
		c, _ := app.ImportQueue.CountByStatus(ctx, id)
		return map[string]int{
			"pending":     c[domain.ImportStatusPending],
			"in_progress": c[domain.ImportStatusInProgress],
			"done":        c[domain.ImportStatusDone],
			"failed":      c[domain.ImportStatusFailed],
			"skipped":     c[domain.ImportStatusSkipped],
		}
	}

	ownAccount := func(r *http.Request) (domain.ExternalAccount, bool) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			return domain.ExternalAccount{}, false
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			return domain.ExternalAccount{}, false
		}
		a, err := app.ExternalAccounts.GetExternalAccount(r.Context(), domain.ExternalAccountID(id))
		if err != nil || a.UserID != userID {
			return domain.ExternalAccount{}, false
		}
		return a, true
	}

	// GET /api/accounts — the user's connected accounts + per-account queue.
	mux.HandleFunc("GET /api/accounts", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		accts, err := app.ExternalAccounts.ListAccountsForUser(r.Context(), userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(accts))
		// Live API budget per provider (memoised — accounts often share one).
		budgets := map[string]map[string]any{}
		budgetLimited := map[string]bool{}
		for _, a := range accts {
			if _, seen := budgets[a.Provider]; !seen {
				budgets[a.Provider], budgetLimited[a.Provider] = providerBudget(r.Context(), app.NATSBus, a.Provider)
			}
		}
		for _, a := range accts {
			// Total activities already imported from this account (count of
			// its sources). Cheap for single-user instances.
			imported := 0
			if existing, e := app.Activities.ExistingExternalIDs(r.Context(), a.Provider, a.ID); e == nil {
				imported = len(existing)
			}
			var lastSync any
			if a.LastSyncAt != nil {
				lastSync = a.LastSyncAt.UTC().Format(time.RFC3339)
			}
			var watermark any
			if a.SyncWatermark != nil {
				watermark = a.SyncWatermark.UTC().Format(time.RFC3339)
			}
			// Per-connection config (currently the poll interval). 0 = use the
			// instance default.
			pollInterval := 0
			providerTotal := 0
			discovering := false
			rateLimited := rateLimitedNow(a.RateLimit) || budgetLimited[a.Provider]
			if len(a.Config) > 0 {
				var cfg map[string]any
				if json.Unmarshal(a.Config, &cfg) == nil {
					if v, ok := cfg["poll_interval_seconds"].(float64); ok {
						pollInterval = int(v)
					}
					if v, ok := cfg["last_discovered_total"].(float64); ok {
						providerTotal = int(v)
					}
					if v, ok := cfg["discovering"].(bool); ok {
						discovering = v
					}
					// Worker-reported budget exhaustion (set by sync/preview|start
					// on a rate-limit error) — surfaced until the cooldown lapses.
					if v, ok := cfg["rate_limited_until"].(string); ok && v != "" {
						if until, err := time.Parse(time.RFC3339, v); err == nil && time.Now().Before(until) {
							rateLimited = true
						}
					}
				}
			}
			// last_discovered_total is a snapshot from the last user-driven
			// discovery; reconcile imports activities without refreshing it,
			// so the live imported count can overtake it ("906 / 905"). An
			// imported external id is proof the provider had at least that
			// many — clamp instead of showing an impossible ratio. Zero stays
			// zero: it means "never discovered" and the UI hides the total.
			if providerTotal > 0 && imported > providerTotal {
				providerTotal = imported
			}
			var connectionID any
			if a.ConnectionID != nil {
				connectionID = a.ConnectionID.String()
			}
			out = append(out, map[string]any{
				"id": a.ID.String(), "provider": a.Provider,
				"connection_id":       connectionID,
				"provider_account_id": a.ProviderAccountID,
				"label":               a.DisplayLabel, "status": string(a.Status),
				"imported":              imported,
				"provider_total":        providerTotal,
				"last_sync_at":          lastSync,
				"watermark":             watermark,
				"rate_limited":          rateLimited,
				"rate_limit_budget":     budgets[a.Provider],
				"discovering":           discovering,
				"poll_interval_seconds": pollInterval,
				"auto_import_enabled":   a.AutoImportEnabled,
				"queue":                 queueCounts(r.Context(), a.ID),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"accounts": out,
			// Instance-wide reconcile cadence — shown next to the per-connection
			// override so "0 = instance default" names a concrete number.
			"default_poll_interval_seconds": int(app.DefaultPollInterval.Seconds()),
		})
	})

	// PUT /api/accounts/{id}/auto-import {enabled} — suspend/resume automatic
	// imports (reconcile + webhook fetch) for this account (#93).
	mux.HandleFunc("PUT /api/accounts/{id}/auto-import", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeJSONBody(r, &body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if err := app.ExternalAccounts.SetAutoImport(r.Context(), a.ID, body.Enabled); err != nil {
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"auto_import_enabled": body.Enabled})
	})

	// POST /api/accounts/{id}/sync/preview — discover + summarize (no enqueue).
	mux.HandleFunc("POST /api/accounts/{id}/sync/preview", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		items, complete, err := discoverAllNoWait(r.Context(), app, a)
		if err != nil {
			if writeRateLimited(w, app, r, a.ID, err) {
				return
			}
			http.Error(w, "discover failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		// Persist the provider-side total so the connection card can show
		// "imported X / Y" even without re-running discovery. A successful
		// discover also clears any stale rate-limit marker.
		_ = app.ExternalAccounts.UpdateAccountConfig(r.Context(), a.ID,
			map[string]any{"last_discovered_total": len(items), "rate_limited_until": ""})
		existing, _ := app.Activities.ExistingExternalIDs(r.Context(), a.Provider, a.ID)
		present := 0
		for _, it := range items {
			if _, ok := existing[it.ExternalID]; ok {
				present++
			}
		}
		// complete=false means a rate limit cut the preview walk short — the
		// counts are a lower bound; the full sync will discover the rest.
		writeJSON(w, http.StatusOK, map[string]any{
			"total": len(items), "already_present": present, "new": len(items) - present,
			"complete": complete,
		})
	})

	// POST /api/accounts/{id}/sync/start {skip_existing} — discover + enqueue.
	mux.HandleFunc("POST /api/accounts/{id}/sync/start", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			SkipExisting bool `json:"skip_existing"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		// Full Sync runs in the background: it walks EVERY page (to the 0-item
		// end), enqueues incrementally, and resumes across rate-limit windows.
		// Returning immediately keeps the request fast; the UI tracks progress
		// via the queue counts + the "discovering" flag.
		go runFullDiscovery(app, logger, a, body.SkipExisting)
		logger.Info("full discovery started", "account_id", a.ID, "skip_existing", body.SkipExisting)
		writeJSON(w, http.StatusOK, map[string]any{"started": true})
	})

	// POST /api/accounts/{id}/import-one {external_id} — explicitly import (or
	// re-import) a single activity by its provider ID. Provider-generic: it
	// rides the same persisted queue → processor → fetch_source job path as
	// full sync, so pacing, rate limits, and result routing all apply.
	mux.HandleFunc("POST /api/accounts/{id}/import-one", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			ExternalID string `json:"external_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		body.ExternalID = strings.TrimSpace(body.ExternalID)
		if body.ExternalID == "" {
			http.Error(w, "external_id required", http.StatusBadRequest)
			return
		}
		n, err := app.ImportQueue.Enqueue(r.Context(), []domain.ImportQueueItem{{
			ExternalAccountID: a.ID, UserID: a.UserID, Provider: a.Provider,
			ItemType: domain.ImportItemActivity, ExternalID: body.ExternalID,
		}})
		if err != nil {
			http.Error(w, "enqueue failed", http.StatusInternalServerError)
			return
		}
		logger.Info("single-activity import queued", "account_id", a.ID, "external_id", body.ExternalID, "new", n > 0)
		writeJSON(w, http.StatusOK, map[string]any{"queued": n > 0, "already_queued": n == 0})
	})

	// PUT /api/accounts/{id}/config — per-connection settings (poll interval).
	mux.HandleFunc("PUT /api/accounts/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		var body struct {
			PollIntervalSeconds *int `json:"poll_interval_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		patch := map[string]any{}
		if body.PollIntervalSeconds != nil {
			v := *body.PollIntervalSeconds
			if v < 0 {
				http.Error(w, "poll_interval_seconds must be >= 0", http.StatusBadRequest)
				return
			}
			patch["poll_interval_seconds"] = v
		}
		if err := app.ExternalAccounts.UpdateAccountConfig(r.Context(), a.ID, patch); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// GET /api/accounts/{id}/history — the connection's import history.
	mux.HandleFunc("GET /api/accounts/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		events := []map[string]any{}
		if app.ImportEvents != nil {
			list, _ := app.ImportEvents.ListForAccount(r.Context(), a.ID, 50)
			for _, e := range list {
				ev := map[string]any{
					"kind":        e.Kind,
					"count":       e.Count,
					"detail":      e.Detail,
					"external_id": e.ExternalID,
					"occurred_at": e.OccurredAt.UTC().Format(time.RFC3339),
				}
				// Resolve the internal activity id for per-activity events so the
				// UI can deep-link to the imported activity, plus an external deep
				// link to the original on the provider's site.
				linked := false
				if e.ExternalID != "" && (e.Kind == domain.ImportEventActivityImported || e.Kind == domain.ImportEventActivityUpdated) {
					if src, err := app.Activities.FindSourceByExternalID(r.Context(), a.Provider, &a.ID, e.ExternalID); err == nil {
						ev["activity_id"] = src.ActivityID.String()
						linked = true
					}
					if ext := providerActivityURL(a.Provider, e.ExternalID); ext != "" {
						ev["external_url"] = ext
					}
				}
				// Per-entry status: failed; completed (the activity is in and
				// linkable); or ongoing (queued/processing — no link yet).
				switch {
				case e.Kind == domain.ImportEventFailed:
					ev["status"] = "failed"
				case linked:
					ev["status"] = "completed"
				default:
					ev["status"] = "ongoing"
				}
				events = append(events, ev)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	})

	// GET /api/accounts/{id}/queue — queue status.
	mux.HandleFunc("GET /api/accounts/{id}/queue", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, queueCounts(r.Context(), a.ID))
	})

	// GET /api/accounts/{id}/queue/items?status=pending,in_progress&limit=100 —
	// the actual queue rows, for debugging a stuck import from the UI. Default
	// filter is the "live" set (everything not done); pass status=all for the
	// full picture including done items.
	mux.HandleFunc("GET /api/accounts/{id}/queue/items", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		statuses := []domain.ImportItemStatus{domain.ImportStatusPending, domain.ImportStatusInProgress, domain.ImportStatusFailed}
		if q := r.URL.Query().Get("status"); q != "" {
			if q == "all" {
				statuses = nil
			} else {
				statuses = statuses[:0]
				for s := range strings.SplitSeq(q, ",") {
					if s = strings.TrimSpace(s); s != "" {
						statuses = append(statuses, domain.ImportItemStatus(s))
					}
				}
			}
		}
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		items, err := app.ImportQueue.ListForAccount(r.Context(), a.ID, statuses, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			row := map[string]any{
				"id":          it.ID.String(),
				"item_type":   string(it.ItemType),
				"external_id": it.ExternalID,
				"status":      string(it.Status),
				"priority":    it.Priority,
				"attempts":    it.Attempts,
				"last_error":  it.LastError,
				"created_at":  it.CreatedAt.UTC().Format(time.RFC3339),
			}
			if it.ItemTime != nil {
				row["item_time"] = it.ItemTime.UTC().Format(time.RFC3339)
			}
			if it.StartedAt != nil {
				row["started_at"] = it.StartedAt.UTC().Format(time.RFC3339)
			}
			if it.CompletedAt != nil {
				row["completed_at"] = it.CompletedAt.UTC().Format(time.RFC3339)
			}
			if url := providerActivityURL(a.Provider, it.ExternalID); url != "" {
				row["external_url"] = url
			}
			out = append(out, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	})

	// POST /api/accounts/{id}/queue/items/{itemID}/requeue — flip one failed
	// item back to pending (attempts reset) so the processor retries it.
	mux.HandleFunc("POST /api/accounts/{id}/queue/items/{itemID}/requeue", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		itemID, err := uuid.Parse(r.PathValue("itemID"))
		if err != nil {
			http.Error(w, "bad item id", http.StatusBadRequest)
			return
		}
		done, err := app.ImportQueue.RequeueFailed(r.Context(), a.ID, domain.ImportQueueItemID(itemID))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !done {
			http.Error(w, "item is not in failed state", http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"requeued": true})
	})

	// POST /api/accounts/{id}/queue/items/{itemID}/move-to-top — bump one
	// pending item above the rest so it is claimed on the next tick.
	mux.HandleFunc("POST /api/accounts/{id}/queue/items/{itemID}/move-to-top", func(w http.ResponseWriter, r *http.Request) {
		a, ok := ownAccount(r)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		itemID, err := uuid.Parse(r.PathValue("itemID"))
		if err != nil {
			http.Error(w, "bad item id", http.StatusBadRequest)
			return
		}
		done, err := app.ImportQueue.MoveToTop(r.Context(), a.ID, domain.ImportQueueItemID(itemID))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !done {
			http.Error(w, "item is not in pending state", http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"moved": true})
	})

	logger.Info("sync endpoints mounted")
}

// discoverItems asks the provider's worker (request/reply) what's importable.
// discoverBatch asks the worker for one batch of activities starting at
// startPage. The worker walks up to discoverMaxPagesPerCall pages and returns
// Complete (reached the 0-item end page), or NextPage + RateLimited so the
// caller can resume.
func discoverBatch(ctx context.Context, app *App, a domain.ExternalAccount, startPage int) (syncDiscoverResp, error) {
	reqBody, _ := json.Marshal(map[string]any{"account_id": a.ID.String(), "start_page": startPage})
	respBytes, err := app.NATSBus.Request(ctx, "cairn.discover."+a.Provider, reqBody, 90*time.Second)
	if err != nil {
		return syncDiscoverResp{}, err
	}
	var resp syncDiscoverResp
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return syncDiscoverResp{}, err
	}
	if resp.Error != "" {
		return syncDiscoverResp{}, fmt.Errorf("worker: %s", resp.Error)
	}
	return resp, nil
}

// discoverAllNoWait walks every page of activities WITHOUT waiting on rate
// limits — for the preview estimate. Returns the items found so far and whether
// discovery reached the true end (complete). When a rate limit is hit mid-walk
// it returns the partial set with complete=false (the count is a lower bound).
func discoverAllNoWait(ctx context.Context, app *App, a domain.ExternalAccount) (items []syncDiscoverItem, complete bool, err error) {
	page := 1
	for iter := 0; iter < 2000; iter++ { // safety bound on round-trips
		b, e := discoverBatch(ctx, app, a, page)
		if e != nil {
			return items, false, e
		}
		items = append(items, b.Items...)
		if b.Complete {
			return items, true, nil
		}
		if b.RateLimited {
			return items, false, nil // partial; preview doesn't block on refills
		}
		page = b.NextPage
		if page <= 0 {
			return items, true, nil
		}
	}
	return items, false, nil
}

// buildImportQueue turns discovered items into queue rows, optionally skipping
// already-imported external ids.
func buildImportQueue(items []syncDiscoverItem, a domain.ExternalAccount, skipExisting bool, existing map[string]struct{}) []domain.ImportQueueItem {
	queue := make([]domain.ImportQueueItem, 0, len(items))
	for _, it := range items {
		if skipExisting {
			if _, present := existing[it.ExternalID]; present {
				continue
			}
		}
		var t *time.Time
		if it.ItemTime != "" {
			if parsed, e := time.Parse(time.RFC3339, it.ItemTime); e == nil {
				t = &parsed
			}
		}
		queue = append(queue, domain.ImportQueueItem{
			ExternalAccountID: a.ID, UserID: a.UserID, Provider: a.Provider,
			ItemType: domain.ImportItemType(it.ItemType), ExternalID: it.ExternalID, ItemTime: t,
		})
	}
	return queue
}

// runFullDiscovery is the background worker behind Full Sync: it walks EVERY
// page of the account's activities (to the 0-item end), enqueuing each batch
// incrementally so the worker can start pulling immediately, and PAUSES then
// RESUMES across provider rate-limit windows until discovery is complete. Runs
// detached from the request (the HTTP call returns "started").
func runFullDiscovery(app *App, logger *slog.Logger, a domain.ExternalAccount, skipExisting bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	log := logger.With("component", "full_discovery", "account_id", a.ID)

	_ = app.ExternalAccounts.UpdateAccountConfig(ctx, a.ID, map[string]any{"discovering": true})
	defer func() {
		_ = app.ExternalAccounts.UpdateAccountConfig(context.Background(), a.ID, map[string]any{"discovering": false})
	}()

	var existing map[string]struct{}
	if skipExisting {
		existing, _ = app.Activities.ExistingExternalIDs(ctx, a.Provider, a.ID)
	}

	page := 1
	discovered, enqueued := 0, 0
	for {
		b, err := discoverBatch(ctx, app, a, page)
		if err != nil {
			log.Warn("discover batch failed; stopping", "page", page, "error", err)
			break
		}
		discovered += len(b.Items)
		if q := buildImportQueue(b.Items, a, skipExisting, existing); len(q) > 0 {
			if n, e := app.ImportQueue.Enqueue(ctx, q); e == nil {
				enqueued += n
			} else {
				log.Warn("enqueue failed", "error", e)
			}
		}
		_ = app.ExternalAccounts.UpdateAccountConfig(ctx, a.ID,
			map[string]any{"last_discovered_total": discovered, "discovering": true})

		if b.Complete {
			_ = app.ExternalAccounts.UpdateAccountConfig(ctx, a.ID, map[string]any{"rate_limited_until": ""})
			break
		}
		page = b.NextPage
		if page <= 0 {
			break
		}
		if b.RateLimited {
			_ = app.ExternalAccounts.UpdateAccountConfig(ctx, a.ID,
				map[string]any{"rate_limited_until": time.Now().UTC().Add(rateLimitCooldown).Format(time.RFC3339)})
			log.Info("discovery paused on rate limit; resuming after window", "next_page", page)
			select {
			case <-time.After(16 * time.Minute): // Strava short window is 15 min
			case <-ctx.Done():
				return
			}
		}
	}

	if app.ImportEvents != nil && enqueued > 0 {
		detail := "Full sync"
		if skipExisting {
			detail = "Full sync (new only)"
		}
		_ = app.ImportEvents.Record(ctx, domain.ConnectionImportEvent{
			ExternalAccountID: a.ID, Kind: domain.ImportEventSyncStarted, Count: enqueued, Detail: detail,
		})
	}
	log.Info("full discovery complete", "discovered", discovered, "enqueued", enqueued)
}

// ---------------------------------------------------------------------------
// Processor goroutine
// ---------------------------------------------------------------------------

const (
	queueMaxInFlightPerAccount = 5
	queueProcessorTick         = 8 * time.Second

	// Items dispatched but without a result for this long are considered
	// stale: the fetch job was Term'd on the worker (DLQ) or its result got
	// lost, and nothing else will ever complete them. They're requeued (or
	// failed at queueStaleMaxAttempts) so they can't pin a queue slot forever.
	queueStaleAfter       = 30 * time.Minute
	queueStaleMaxAttempts = 5
	queueStaleReapEvery   = 5 * time.Minute
)

// runImportQueueProcessor drains the persisted import queue, paced and
// rate-limit-aware. Each tick it scans accounts with pending items and, for
// each that isn't rate-limited and has capacity, claims a batch and dispatches
// fetch jobs. Items stay in_progress until the result router marks them done.
func runImportQueueProcessor(ctx context.Context, logger *slog.Logger, app *App) {
	log := logger.With("component", "import_queue_processor")
	log.Info("import queue processor started", "tick", queueProcessorTick)
	ticker := time.NewTicker(queueProcessorTick)
	defer ticker.Stop()
	reap := time.NewTicker(queueStaleReapEvery)
	defer reap.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("import queue processor shutting down")
			return
		case <-reap.C:
			requeued, failed, err := app.ImportQueue.RequeueStale(ctx, queueStaleAfter, queueStaleMaxAttempts)
			if err != nil {
				log.Warn("stale queue reap failed", "error", err)
			} else if requeued > 0 || failed > 0 {
				log.Info("stale queue items reaped", "requeued", requeued, "failed", failed)
			}
		case <-ticker.C:
			accts, err := app.ImportQueue.AccountsWithPending(ctx, 100)
			if err != nil {
				log.Warn("scan pending accounts failed", "error", err)
				continue
			}
			for _, acctID := range accts {
				processAccountQueue(ctx, log, app, acctID)
			}
		}
	}
}

func processAccountQueue(ctx context.Context, log *slog.Logger, app *App, acctID domain.ExternalAccountID) {
	acct, err := app.ExternalAccounts.GetExternalAccount(ctx, acctID)
	if err != nil {
		return
	}
	if rateLimitedNow(acct.RateLimit) {
		return // paused until the window resets
	}

	counts, err := app.ImportQueue.CountByStatus(ctx, acctID)
	if err != nil {
		return
	}
	avail := queueMaxInFlightPerAccount - counts[domain.ImportStatusInProgress]
	if avail <= 0 {
		return
	}

	items, err := app.ImportQueue.ClaimPending(ctx, acctID, avail)
	if err != nil || len(items) == 0 {
		return
	}
	for _, it := range items {
		body, _ := json.Marshal(fetchSourceJobBody{
			JobID:        it.ID.String(),
			AccountID:    acctID.String(),
			UserID:       it.UserID.String(),
			Provider:     it.Provider,
			ExtID:        it.ExternalID,
			FetchStreams: true,
			Reason:       "queue",
		})
		msgID := "queue:" + it.Provider + ":" + acctID.String() + ":" + it.ExternalID
		if err := app.NATSBus.Publish(ctx, "cairn.jobs.fetch_source."+it.Provider, msgID, body); err != nil {
			_ = app.ImportQueue.MarkFailed(ctx, it.ID, "dispatch: "+err.Error())
		}
		// Item stays in_progress; result_router marks it done after ingest.
	}
	log.Info("dispatched queue items", "account_id", acctID, "n", len(items))
}

// rateLimitCooldown is how long a connection is marked rate-limited in the UI
// after the worker reports an exhausted provider budget. The worker doesn't
// return a precise reset time, so we use a conservative cooldown; a successful
// discover clears it early.
const rateLimitCooldown = 30 * time.Minute

// isRateLimitErr reports whether a discover error is the worker's
// provider-budget-exhausted signal (vs a real failure).
func isRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "rate_limited") || strings.Contains(s, "budget exhausted") ||
		strings.Contains(s, "rate limited")
}

// writeRateLimited, when err is a rate-limit signal, persists a cooldown marker
// on the account (so /api/accounts reports rate_limited=true and the status
// badge shows it) and responds 429 with a clear message. Returns true when it
// handled the error.
func writeRateLimited(w http.ResponseWriter, app *App, r *http.Request, id domain.ExternalAccountID, err error) bool {
	if !isRateLimitErr(err) {
		return false
	}
	until := time.Now().UTC().Add(rateLimitCooldown)
	_ = app.ExternalAccounts.UpdateAccountConfig(r.Context(), id,
		map[string]any{"rate_limited_until": until.Format(time.RFC3339)})
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"error":   "rate_limited",
		"message": "Provider daily/short budget exhausted — no new activities can be fetched right now. Try again later.",
		"until":   until.Format(time.RFC3339),
	})
	return true
}

// rateLimitKVState mirrors the worker rate limiter's KV value (the provider
// repos' internal/nats rateLimitState). The worker syncs it from the
// provider's usage headers, so it's the live view of the real API budget.
type rateLimitKVState struct {
	Capacity      int       `json:"capacity"`
	Used          int       `json:"used"`
	WindowResetAt time.Time `json:"window_reset_at"`
}

// kvBus is the slice of the NATS bus providerBudget needs — taking the
// interface (not *App) lets wire.go build the reconcile budget gate before
// the App struct exists.
type kvBus interface {
	KV(bucket string) (port.KV, error)
}

// providerBudget reads the provider's live API budget from the shared
// cairn_rate_limits KV (keys <provider>.short / <provider>.daily). Returns a
// UI-friendly map per window plus whether any window is currently exhausted —
// this is what flips the connection badge to "Rate limited" while the worker
// waits out a 429, and what the reconcile scheduler's budget gate keys on.
// Missing bucket/keys mean "no worker has reported yet".
func providerBudget(ctx context.Context, bus kvBus, provider string) (map[string]any, bool) {
	if bus == nil {
		return nil, false
	}
	kv, err := bus.KV("cairn_rate_limits")
	if err != nil {
		return nil, false
	}
	out := map[string]any{}
	limited := false
	now := time.Now().UTC()
	for _, win := range []string{"short", "daily"} {
		entry, err := kv.Get(ctx, provider+"."+win)
		if err != nil {
			continue
		}
		var st rateLimitKVState
		if json.Unmarshal(entry.Value, &st) != nil || st.Capacity <= 0 {
			continue
		}
		active := now.Before(st.WindowResetAt)
		used := st.Used
		if !active {
			used = 0 // window elapsed; budget is fresh even if no call re-synced yet
		}
		out[win] = map[string]any{
			"used":      used,
			"limit":     st.Capacity,
			"resets_at": st.WindowResetAt.UTC().Format(time.RFC3339),
		}
		if active && used >= st.Capacity {
			limited = true
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, limited
}

// rateLimitedNow reports whether either provider window is exhausted and not
// yet reset.
func rateLimitedNow(rl *domain.RateLimitSnapshot) bool {
	if rl == nil {
		return false
	}
	now := time.Now()
	if rl.ShortWindowLimit > 0 && rl.ShortWindowUsed >= rl.ShortWindowLimit && now.Before(rl.ShortWindowResetAt) {
		return true
	}
	if rl.LongWindowLimit > 0 && rl.LongWindowUsed >= rl.LongWindowLimit && now.Before(rl.LongWindowResetAt) {
		return true
	}
	return false
}

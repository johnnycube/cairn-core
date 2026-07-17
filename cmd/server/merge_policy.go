package main

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/domain"
)

// mountMergePolicy configures the priority cascade (per-user → instance default
// → AnyProvider; per-field manual pins beat all). A policy is
// {default_priority:[...], overrides:{field:[...]}}.
//
//	GET/PUT /api/settings/merge-policy             (user)
//	GET/PUT /api/admin/instance/merge-defaults     (admin)
func mountMergePolicy(mux *http.ServeMux, app *App, logger *slog.Logger) {
	if app.UserSettings == nil {
		return
	}

	mux.HandleFunc("GET /api/settings/merge-policy", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		policies, err := app.UserSettings.GetAllMergePolicies(r.Context(), userID)
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		defaults, err := app.UserSettings.GetInstanceMergeDefaults(r.Context())
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"policies":          policiesToAPI(policies),
			"instance_defaults": policiesToAPI(defaults),
			"field_groups":      fieldGroupKeys(),
		})
	})

	mux.HandleFunc("PUT /api/settings/merge-policy", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		policies, ok := decodePoliciesBody(w, r)
		if !ok {
			return
		}
		if err := app.UserSettings.SetMergePolicies(r.Context(), userID, policies); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		logger.Info("user merge policy updated", "user_id", userID, "types", len(policies))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/admin/instance/merge-defaults", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		defaults, err := app.UserSettings.GetInstanceMergeDefaults(r.Context())
		if err != nil {
			http.Error(w, "load failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"policies": policiesToAPI(defaults), "field_groups": fieldGroupKeys()})
	})

	mux.HandleFunc("PUT /api/admin/instance/merge-defaults", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdminUser(r, app); !ok {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		policies, ok := decodePoliciesBody(w, r)
		if !ok {
			return
		}
		if err := app.UserSettings.SetInstanceMergeDefaults(r.Context(), policies); err != nil {
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		logger.Info("instance merge defaults updated", "types", len(policies))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

type policyAPI struct {
	DefaultPriority []string            `json:"default_priority"`
	Overrides       map[string][]string `json:"overrides,omitempty"`
}

func policiesToAPI(m map[domain.ActivityType]domain.MergePolicy) map[string]policyAPI {
	out := make(map[string]policyAPI, len(m))
	for t, p := range m {
		api := policyAPI{DefaultPriority: p.DefaultPriority}
		if len(p.Overrides) > 0 {
			api.Overrides = make(map[string][]string, len(p.Overrides))
			for k, v := range p.Overrides {
				api.Overrides[string(k)] = v
			}
		}
		out[string(t)] = api
	}
	return out
}

func decodePoliciesBody(w http.ResponseWriter, r *http.Request) (map[domain.ActivityType]domain.MergePolicy, bool) {
	var body struct {
		Policies map[string]policyAPI `json:"policies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return nil, false
	}
	out := make(map[domain.ActivityType]domain.MergePolicy, len(body.Policies))
	for t, api := range body.Policies {
		mp := domain.MergePolicy{DefaultPriority: api.DefaultPriority}
		if len(api.Overrides) > 0 {
			mp.Overrides = make(map[domain.FieldGroup][]string, len(api.Overrides))
			for k, v := range api.Overrides {
				fg := domain.FieldGroup(k)
				if !validFieldGroup(fg) {
					http.Error(w, "unknown field group: "+k, http.StatusBadRequest)
					return nil, false
				}
				mp.Overrides[fg] = v
			}
		}
		out[domain.ActivityType(t)] = mp
	}
	return out, true
}

func fieldGroupKeys() []string {
	out := make([]string, 0, len(domain.AllFieldGroups))
	for _, g := range domain.AllFieldGroups {
		out = append(out, string(g))
	}
	return out
}

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	fakeTokenUserID    = "UTOKEN00001"
	fakeTokenUserEmail = "terraform@example.com"
)

type fakeOwner struct {
	Email          string
	UserID         string
	PermissionType string
}

type fakeApp struct {
	Manifest interface{}
	Owners   []fakeOwner
	Approved bool
}

type fakeApproval struct {
	ID     string
	AppID  string
	Status string
}

// fakeSlack is an in-memory Slack API implementing the endpoints the provider
// calls, with the error behaviors observed against the real API (app_not_found
// for missing and inaccessible apps alike, user_already_owner on duplicate
// collaborators, the token user auto-added as owner on app creation).
type fakeSlack struct {
	mu sync.Mutex

	// requireApproval makes apps.developerInstall fail until an approval
	// request for the app has been granted.
	requireApproval bool
	// stayPending keeps approval requests pending forever instead of granting
	// them on the first poll.
	stayPending bool

	apps      map[string]*fakeApp
	approvals []*fakeApproval
	// lastUpdatedManifest is the raw manifest string received by the most
	// recent apps.manifest.update call, before enrichment.
	lastUpdatedManifest string
	nextApp             int
	nextUser            int
	nextReq             int

	server *httptest.Server
}

func newFakeSlack(t *testing.T, requireApproval bool) *fakeSlack {
	f := &fakeSlack{
		requireApproval: requireApproval,
		apps:            map[string]*fakeApp{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeSlack) url() string { return f.server.URL + "/" }

func (f *fakeSlack) lastUpdate() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastUpdatedManifest
}

func (f *fakeSlack) approvalCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.approvals)
}

func (f *fakeSlack) dropOwner(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, app := range f.apps {
		for i, owner := range app.Owners {
			if owner.Email == email {
				app.Owners = append(app.Owners[:i], app.Owners[i+1:]...)
				break
			}
		}
	}
}

func (f *fakeSlack) addOwner(email string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, app := range f.apps {
		f.nextUser++
		app.Owners = append(app.Owners, fakeOwner{
			Email:          email,
			UserID:         fmt.Sprintf("U%07d", f.nextUser),
			PermissionType: "owner",
		})
	}
}

func (f *fakeSlack) hasOwner(email string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, app := range f.apps {
		for _, owner := range app.Owners {
			if owner.Email == email {
				return true
			}
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	_ = json.NewEncoder(w).Encode(v)
}

func fakeAPIError(w http.ResponseWriter, code string) {
	writeJSON(w, map[string]interface{}{"ok": false, "error": code})
}

// requestParams decodes either a JSON body (bearer-authenticated calls) or a
// form body (token-in-form calls) into a flat map.
func requestParams(r *http.Request) map[string]interface{} {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var m map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&m)
		return m
	}
	_ = r.ParseForm()
	m := map[string]interface{}{}
	for key := range r.Form {
		m[key] = r.Form.Get(key)
	}
	return m
}

func param(m map[string]interface{}, key string) string {
	s, _ := m[key].(string)
	return s
}

func (f *fakeSlack) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	p := requestParams(r)
	switch r.URL.Path {
	case "/auth.test":
		writeJSON(w, map[string]interface{}{"ok": true, "user_id": fakeTokenUserID, "team_id": "T0000001"})

	case "/apps.manifest.create":
		var manifest interface{}
		if err := json.Unmarshal([]byte(param(p, "manifest")), &manifest); err != nil {
			fakeAPIError(w, "invalid_manifest")
			return
		}
		f.nextApp++
		id := fmt.Sprintf("A%07d", f.nextApp)
		f.apps[id] = &fakeApp{
			// Slack enriches stored manifests with server-side defaults.
			Manifest: applyManifestDefaults(manifest),
			Owners:   []fakeOwner{{Email: fakeTokenUserEmail, UserID: fakeTokenUserID, PermissionType: "owner"}},
		}
		writeJSON(w, map[string]interface{}{
			"ok":     true,
			"app_id": id,
			"credentials": map[string]string{
				"client_id":          "client-id-" + id,
				"client_secret":      "client-secret-" + id,
				"verification_token": "verification-token-" + id,
				"signing_secret":     "signing-secret-" + id,
			},
			"oauth_authorize_url": "https://slack.example.com/oauth/" + id,
		})

	case "/apps.manifest.export":
		app, ok := f.apps[param(p, "app_id")]
		if !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "manifest": app.Manifest})

	case "/apps.manifest.update":
		app, ok := f.apps[param(p, "app_id")]
		if !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		var manifest interface{}
		if err := json.Unmarshal([]byte(param(p, "manifest")), &manifest); err != nil {
			fakeAPIError(w, "invalid_manifest")
			return
		}
		f.lastUpdatedManifest = param(p, "manifest")
		app.Manifest = applyManifestDefaults(manifest)
		writeJSON(w, map[string]interface{}{"ok": true})

	case "/apps.manifest.delete":
		if _, ok := f.apps[param(p, "app_id")]; !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		delete(f.apps, param(p, "app_id"))
		writeJSON(w, map[string]interface{}{"ok": true})

	case "/developer.apps.owners.list":
		app, ok := f.apps[param(p, "app_id")]
		if !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		owners := []map[string]string{}
		for _, owner := range app.Owners {
			owners = append(owners, map[string]string{
				"user_email":      owner.Email,
				"user_id":         owner.UserID,
				"permission_type": owner.PermissionType,
			})
		}
		writeJSON(w, map[string]interface{}{"ok": true, "owners": owners})

	case "/developer.apps.owners.add":
		app, ok := f.apps[param(p, "app_id")]
		if !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		email := param(p, "user_email")
		for _, owner := range app.Owners {
			if owner.Email == email {
				fakeAPIError(w, "user_already_owner")
				return
			}
		}
		f.nextUser++
		app.Owners = append(app.Owners, fakeOwner{
			Email:          email,
			UserID:         fmt.Sprintf("U%07d", f.nextUser),
			PermissionType: param(p, "permission_type"),
		})
		writeJSON(w, map[string]interface{}{"ok": true})

	case "/developer.apps.owners.remove":
		app, ok := f.apps[param(p, "app_id")]
		if !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		email := param(p, "user_email")
		for i, owner := range app.Owners {
			if owner.Email == email {
				app.Owners = append(app.Owners[:i], app.Owners[i+1:]...)
				writeJSON(w, map[string]interface{}{"ok": true})
				return
			}
		}
		fakeAPIError(w, "user_not_owner")

	case "/apps.developerInstall":
		app, ok := f.apps[param(p, "app_id")]
		if !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		if f.requireApproval && !app.Approved {
			fakeAPIError(w, "install_not_approved")
			return
		}
		// developerInstall is idempotent: repeated installs re-issue the same
		// tokens for the app, matching observed real-API behavior. A user
		// token is only issued when user scopes were requested, and an
		// app-level token only when the manifest enables Socket Mode.
		appID := param(p, "app_id")
		tokens := map[string]string{"bot": "xoxb-fake-" + appID}
		if userScopes, _ := p["user_scopes"].([]interface{}); len(userScopes) > 0 {
			tokens["user"] = "xoxp-fake-" + appID
		}
		if m, ok := app.Manifest.(map[string]interface{}); ok {
			if settings, ok := m["settings"].(map[string]interface{}); ok {
				if enabled, _ := settings["socket_mode_enabled"].(bool); enabled {
					tokens["app_level"] = "xapp-fake-" + appID
				}
			}
		}
		writeJSON(w, map[string]interface{}{
			"ok":                true,
			"api_access_tokens": tokens,
		})

	case "/apps.approvals.requests.create":
		appID := param(p, "app")
		if _, ok := f.apps[appID]; !ok {
			fakeAPIError(w, "app_not_found")
			return
		}
		f.nextReq++
		request := &fakeApproval{ID: fmt.Sprintf("R%07d", f.nextReq), AppID: appID, Status: "pending"}
		f.approvals = append(f.approvals, request)
		writeJSON(w, map[string]interface{}{"ok": true, "request_id": request.ID})

	case "/apps.approvals.requests.list":
		appID := param(p, "app_id")
		requests := []map[string]string{}
		for _, request := range f.approvals {
			if request.AppID != appID {
				continue
			}
			// Approvals are granted on the first poll to keep tests fast.
			if request.Status == "pending" && !f.stayPending {
				request.Status = "approved"
				if app, ok := f.apps[appID]; ok {
					app.Approved = true
				}
			}
			requests = append(requests, map[string]string{"id": request.ID, "status": request.Status})
		}
		writeJSON(w, map[string]interface{}{"ok": true, "requests": requests})

	case "/apps.approvals.requests.cancel":
		writeJSON(w, map[string]interface{}{"ok": true})

	default:
		fakeAPIError(w, "unknown_method")
	}
}

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/version"
)

func TestAppVersionAndUpdateCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/empty":
			_, _ = w.Write([]byte(`[]`))
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = w.Write([]byte(`{"tag_name":"v99.0.0","html_url":"https://example.org/r","body":"notes"}`))
		}
	}))
	defer srv.Close()
	a := newTestApp(t, func(a *App) error { a.opts.UpdateSource = srv.URL + "/latest"; return nil })

	var vi VersionInfo
	call(t, a, "app.version", nil, &vi)
	if vi.Version != version.Version || vi.String == "" || vi.APIVersion != Version || vi.Dev != version.IsDev() {
		t.Fatalf("version %+v", vi)
	}
	var res UpdateCheckResult
	call(t, a, "update.check", nil, &res)
	if !res.Available || res.Latest != "99.0.0" || res.URL != "https://example.org/r" || res.Notes != "notes" || res.Source != srv.URL+"/latest" {
		t.Fatalf("update %+v", res)
	}
	wantCode(t, a, "update.check", UpdateCheckParams{Source: srv.URL + "/empty"}, CodeNotFound)
	wantCode(t, a, "update.check", UpdateCheckParams{Source: srv.URL + "/broken"}, CodeUnavailable)
}

package update

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const latestJSON = `{
  "tag_name": "v1.4.0",
  "name": "Tiegel 1.4.0",
  "html_url": "https://github.com/ape-dev-de/Tiegel/releases/tag/v1.4.0",
  "body": "- faster editor\n- fixes",
  "draft": false,
  "prerelease": false,
  "assets": [{"name": "Tiegel.dmg"}]
}`

const listJSON = `[
  {"tag_name": "v1.5.0-rc.1", "html_url": "https://example.org/rc", "prerelease": true},
  {"tag_name": "v2.0.0", "html_url": "https://example.org/draft", "draft": true},
  {"tag_name": "nightly", "html_url": "https://example.org/nightly"},
  {"tag_name": "v1.4.1", "html_url": "https://example.org/141", "body": "patch"},
  {"tag_name": "v1.3.0", "html_url": "https://example.org/130"}
]`

// serve starts a test server answering every request with body and records
// the last request.
func serve(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	var last http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = *r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestDisabledByDefault(t *testing.T) {
	var cfg Config
	if cfg.Enabled {
		t.Fatal("zero Config must be disabled")
	}
	called := false
	c := New(cfg, doerFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("must not be called")
	}))
	_, err := c.Check(context.Background(), "v1.0.0")
	if !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
	if called {
		t.Fatal("disabled check performed a request")
	}
}

func TestCheckSingleRelease(t *testing.T) {
	srv, last := serve(t, http.StatusOK, latestJSON)
	c := New(Config{Enabled: true, Source: srv.URL + "/releases/latest"}, srv.Client())

	res, err := c.Check(context.Background(), "v1.3.2")
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Available: true,
		Version:   "1.4.0",
		URL:       "https://github.com/ape-dev-de/Tiegel/releases/tag/v1.4.0",
		Notes:     "- faster editor\n- fixes",
	}
	if res != want {
		t.Fatalf("res = %+v, want %+v", res, want)
	}

	// Privacy: generic UA, no query, no cookies, no version in the request.
	if ua := last.Header.Get("User-Agent"); ua != UserAgent {
		t.Errorf("User-Agent = %q, want %q", ua, UserAgent)
	}
	if last.URL.RawQuery != "" {
		t.Errorf("query = %q, want none", last.URL.RawQuery)
	}
	if last.Header.Get("Cookie") != "" {
		t.Error("request carries cookies")
	}
	for k, vals := range last.Header {
		for _, v := range vals {
			if strings.Contains(v, "1.3.2") {
				t.Errorf("header %s leaks the current version: %q", k, v)
			}
		}
	}
	if last.Method != http.MethodGet {
		t.Errorf("method = %s", last.Method)
	}
}

func TestCheckUpToDate(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, latestJSON)
	c := New(Config{Enabled: true, Source: srv.URL}, srv.Client())
	for _, cur := range []string{"v1.4.0", "1.4.0", "v1.4.0-3-gabcdef0", "v1.5.0", "v2.0.0-rc.1"} {
		res, err := c.Check(context.Background(), cur)
		if err != nil {
			t.Fatal(err)
		}
		if res.Available {
			t.Errorf("current %s: Available = true, want false", cur)
		}
		if res.Version != "1.4.0" {
			t.Errorf("Version = %q", res.Version)
		}
	}
	// A pre-release of the latest version is older.
	res, err := c.Check(context.Background(), "v1.4.0-rc.2")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Error("v1.4.0-rc.2 should see v1.4.0 as update")
	}
}

func TestCheckDevBuild(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, latestJSON)
	c := New(Config{Enabled: true, Source: srv.URL}, srv.Client())
	res, err := c.Check(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatal("dev build should be offered the latest release")
	}
}

func TestCheckList(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, listJSON)

	c := New(Config{Enabled: true, Source: srv.URL}, srv.Client())
	res, err := c.Check(context.Background(), "v1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "1.4.1" || res.URL != "https://example.org/141" || res.Notes != "patch" || !res.Available {
		t.Fatalf("res = %+v", res)
	}

	c = New(Config{Enabled: true, Source: srv.URL, IncludePrereleases: true}, srv.Client())
	res, err = c.Check(context.Background(), "v1.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "1.5.0-rc.1" || !res.Available {
		t.Fatalf("with prereleases: res = %+v", res)
	}
}

func TestCheckNowIgnoresEnabled(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, latestJSON)
	c := New(Config{Source: srv.URL}, srv.Client())
	res, err := c.CheckNow(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available {
		t.Fatal("expected update")
	}
}

func TestUnsafeReleaseURLDropped(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, `{"tag_name":"v9.0.0","html_url":"javascript:alert(1)"}`)
	c := New(Config{Enabled: true, Source: srv.URL}, srv.Client())
	res, err := c.Check(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "" {
		t.Fatalf("URL = %q, want empty", res.URL)
	}
}

func TestCheckErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"status", http.StatusNotFound, `{"message":"Not Found"}`, nil},
		{"bad json object", http.StatusOK, `{"tag_name":`, nil},
		{"bad json list", http.StatusOK, `[{"tag_name":1}]`, nil},
		{"no usable release", http.StatusOK, `[{"tag_name":"nightly"}]`, ErrNoRelease},
		{"empty list", http.StatusOK, `[]`, ErrNoRelease},
		{"too large", http.StatusOK, `{"body":"` + strings.Repeat("x", maxBody) + `"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := serve(t, tt.status, tt.body)
			c := New(Config{Enabled: true, Source: srv.URL}, srv.Client())
			_, err := c.Check(context.Background(), "v1.0.0")
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestTransportError(t *testing.T) {
	c := New(Config{Enabled: true, Source: "https://updates.example.org/feed.json"},
		doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }))
	if _, err := c.Check(context.Background(), "v1.0.0"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadError(t *testing.T) {
	c := New(Config{Enabled: true, Source: "https://updates.example.org/feed.json"},
		doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(errReader{})}, nil
		}))
	if _, err := c.Check(context.Background(), "v1.0.0"); err == nil || !strings.Contains(err.Error(), "read response") {
		t.Fatalf("err = %v", err)
	}
}

func TestContextCanceled(t *testing.T) {
	srv, _ := serve(t, http.StatusOK, latestJSON)
	c := New(Config{Enabled: true, Source: srv.URL}, srv.Client())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Check(ctx, "v1.0.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSource(t *testing.T) {
	if got := New(Config{}, nil).Source(); got != DefaultSource {
		t.Fatalf("default source = %q", got)
	}
	if got := New(Config{Source: "  https://x.example/r.json "}, nil).Source(); got != "https://x.example/r.json" {
		t.Fatalf("custom source = %q", got)
	}
	if New(Config{}, nil).client == nil {
		t.Fatal("nil client not replaced")
	}
}

func TestValidateSource(t *testing.T) {
	ok := []string{
		"https://api.github.com/repos/a/b/releases/latest",
		"https://updates.example.org/tiegel.json",
		"http://localhost:8080/r.json",
		"http://127.0.0.1/r.json",
		"http://[::1]:9000/r.json",
	}
	for _, s := range ok {
		if err := validateSource(s); err != nil {
			t.Errorf("validateSource(%q): %v", s, err)
		}
	}
	bad := []string{
		"http://updates.example.org/r.json",
		"ftp://example.org/r.json",
		"file:///etc/passwd",
		"https:///nohost",
		"://broken",
	}
	for _, s := range bad {
		if err := validateSource(s); err == nil {
			t.Errorf("validateSource(%q) = nil, want error", s)
		}
	}
	c := New(Config{Enabled: true, Source: "http://updates.example.org/r.json"},
		doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("request must not be sent to insecure source")
			return nil, nil
		}))
	if _, err := c.Check(context.Background(), "v1"); err == nil {
		t.Fatal("expected error for insecure source")
	}
}

func TestBadRequestURL(t *testing.T) {
	// Passes validation but cannot be turned into a request.
	c := New(Config{Enabled: true, Source: "https://exa mple.org/"}, nil)
	if _, err := c.Check(context.Background(), "v1"); err == nil {
		t.Fatal("expected error")
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

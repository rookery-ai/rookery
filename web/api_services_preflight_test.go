package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type preflightDTO struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
}

type providerDTO struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Kind        string         `json:"kind"`
	RedirectURI string         `json:"redirect_uri"`
	AppProvider string         `json:"app_provider"`
	AppLabel    string         `json:"app_label"`
	SetupMode   string         `json:"setup_mode"`
	HasCreds    bool           `json:"has_creds"`
	Preflight   []preflightDTO `json:"preflight"`
}

type servicesDTO struct {
	Providers []providerDTO `json:"providers"`
	Summary   struct {
		BaseURL        string `json:"base_url"`
		OAuthProviders int    `json:"oauth_providers"`
		CleanProviders int    `json:"clean_providers"`
	} `json:"summary"`
}

func listServices(t *testing.T) (*Server, servicesDTO) {
	t.Helper()
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)
	// The operator's own ROOKERY_PUBLIC_URL must not leak into the test's expectations.
	t.Setenv("ROOKERY_PUBLIC_URL", "")

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body servicesDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return s, body
}

func findProvider(t *testing.T, body servicesDTO, name string) providerDTO {
	t.Helper()
	for _, p := range body.Providers {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("provider %q not in list", name)
	return providerDTO{}
}

// Every OAuth provider must carry the exact redirect URI the user has to
// register. Its absence is what made OAuth unusable through the UI.
func TestListServicesCarriesRedirectURI(t *testing.T) {
	_, body := listServices(t)

	google := findProvider(t, body, "google")
	want := "/dashboard/connectors/services/callback/google"
	if !strings.HasSuffix(google.RedirectURI, want) {
		t.Fatalf("google redirect_uri = %q, want it to end with %q", google.RedirectURI, want)
	}

	// An api_key provider has no redirect URI at all — emitting one would tell
	// the user to register something that is never used.
	stripe := findProvider(t, body, "stripe")
	if stripe.RedirectURI != "" {
		t.Fatalf("api_key provider must not carry a redirect_uri, got %q", stripe.RedirectURI)
	}

	// httptest requests arrive as http://example.com, a public domain over plain
	// http, which google's verified policy hard-blocks.
	if len(google.Preflight) == 0 {
		t.Fatalf("expected a preflight problem for google over plain http")
	}
	if google.Preflight[0].Severity != "hard" || google.Preflight[0].Fix == "" {
		t.Fatalf("got preflight %+v", google.Preflight[0])
	}
}

// An aliased child authenticates through its PARENT's OAuth application, so its
// redirect URI must name the parent.
//
// This assertion is inverted from what it originally pinned ("a child registers
// its OWN callback path"), because that was the defect rather than the contract.
// Google Cloud only ever had …/callback/google registered, so sending
// …/callback/google_drive was rejected with redirect_uri_mismatch — at the
// CONSENT screen, before a code is issued and therefore before explainOAuthError
// could translate it. Every one of the thirteen aliased providers was unusable.
// Scoping the URI to the OAuth app means one registered URI covers the parent and
// all its children, and an install that already set up Gmail needs no console
// visit at all. See web/oauth_parent_redirect_test.go for the resolution table.
func TestListServicesChildSharesTheParentRedirectPath(t *testing.T) {
	_, body := listServices(t)

	for _, child := range []string{"google_drive", "google_calendar", "youtube"} {
		p := findProvider(t, body, child)
		if !strings.HasSuffix(p.RedirectURI, "/callback/google") {
			t.Errorf("%s redirect_uri = %q, want it to end in /callback/google", child, p.RedirectURI)
		}
	}

	// The parent keeps its own path — that is the URI existing installs registered.
	parent := findProvider(t, body, "google")
	if !strings.HasSuffix(parent.RedirectURI, "/callback/google") {
		t.Errorf("google redirect_uri = %q", parent.RedirectURI)
	}
}

// TestListServicesReportsTheOwningAppAndSetupMode covers the fields the wizard
// needs to say WHICH application to edit. A child inherits its parent's stored
// credentials, so it reports setup_mode "update" even on a first visit — correct,
// because the user must edit the existing app rather than create a second one.
func TestListServicesReportsTheOwningAppAndSetupMode(t *testing.T) {
	_, body := listServices(t)

	child := findProvider(t, body, "google_drive")
	if child.AppProvider != "google" {
		t.Errorf("google_drive app_provider = %q, want google", child.AppProvider)
	}
	if child.AppLabel == "" || child.AppLabel == child.Label {
		t.Errorf("google_drive app_label = %q, want the parent's label", child.AppLabel)
	}

	// No credentials are stored in this fixture, so both report "create".
	if child.SetupMode != setupMode(child.HasCreds) {
		t.Errorf("google_drive setup_mode = %q, inconsistent with has_creds=%v",
			child.SetupMode, child.HasCreds)
	}

	parent := findProvider(t, body, "google")
	if parent.AppProvider != "google" {
		t.Errorf("google app_provider = %q, want itself", parent.AppProvider)
	}
}

func TestListServicesSummaryCountsCleanProviders(t *testing.T) {
	_, body := listServices(t)
	if body.Summary.OAuthProviders < 10 {
		t.Fatalf("expected the bundled OAuth providers to be counted, got %d", body.Summary.OAuthProviders)
	}
	if body.Summary.CleanProviders > body.Summary.OAuthProviders {
		t.Fatalf("clean (%d) cannot exceed total (%d)",
			body.Summary.CleanProviders, body.Summary.OAuthProviders)
	}
	if body.Summary.BaseURL == "" {
		t.Fatalf("summary must carry the base URL it judged")
	}
}

// The echo endpoint is unauthenticated, so single-use and expiry ARE its access
// control and must be pinned by tests.
func TestEchoNonceIsSingleUseAndScoped(t *testing.T) {
	s, _ := newAPITestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=bogus", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unissued token: status %d, want 404", rec.Code)
	}

	s.echoMu.Lock()
	if s.echoNonces == nil {
		s.echoNonces = map[string]echoNonce{}
	}
	s.echoNonces["tok1"] = echoNonce{expires: time.Now().Add(30 * time.Second)}
	s.echoMu.Unlock()

	if rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=tok1", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("issued token: status %d, want 200", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=tok1", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("replayed token: status %d, want 404 — nonces must be single-use", rec.Code)
	}
}

func TestEchoNonceExpires(t *testing.T) {
	s, _ := newAPITestServer(t)
	s.echoMu.Lock()
	if s.echoNonces == nil {
		s.echoNonces = map[string]echoNonce{}
	}
	s.echoNonces["stale"] = echoNonce{expires: time.Now().Add(-time.Second)}
	s.echoMu.Unlock()

	if rec := doJSON(t, s, http.MethodGet, "/healthz/echo?token=stale", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("expired token: status %d, want 404", rec.Code)
	}
}

// Saving an instance URL must change what the redirect URI reports, and a
// malformed value must be rejected rather than silently stored.
func TestSavePublicURLDrivesTheRedirectURI(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)
	t.Setenv("ROOKERY_PUBLIC_URL", "")

	rec := doJSON(t, s, http.MethodPut, "/api/v1/admin/public-url",
		map[string]string{"url": "not-a-url"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed url: status %d, want 400", rec.Code)
	}

	rec = doJSON(t, s, http.MethodPut, "/api/v1/admin/public-url",
		map[string]string{"url": "https://agents.example.com/"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), `"public_url_source":"configured"`) {
		t.Fatalf("source should be configured: %s", rec.Body.String())
	}

	listRec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	var body servicesDTO
	if err := json.Unmarshal(listRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	google := findProvider(t, body, "google")
	want := "https://agents.example.com/dashboard/connectors/services/callback/google"
	if google.RedirectURI != want {
		t.Fatalf("redirect_uri = %q, want %q", google.RedirectURI, want)
	}
	// A configured public https domain satisfies google's policy, so the hard
	// block from the detected http://example.com must be gone.
	if len(google.Preflight) != 0 {
		t.Fatalf("expected a clean preflight, got %+v", google.Preflight)
	}
}

// The untrusted-certificate branch exists specifically so a private-CA install
// is not told its working setup is "unreachable". It is the one outcome that
// cannot be reached by unit-testing publicurl, and it depends on errors.As
// traversing *url.Error → *tls.CertificateVerificationError → the value-typed
// x509.UnknownAuthorityError. If that chain does not match, this silently
// degrades into the false negative it was written to prevent.
func TestSelfTestTreatsUntrustedCertAsWarningNotFailure(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	// httptest's TLS server uses a self-signed cert the default client rejects.
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tls.Close()

	rec := doJSON(t, s, http.MethodPost, "/api/v1/admin/public-url/test",
		map[string]string{"url": tls.URL}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK      bool   `json:"ok"`
		Warning bool   `json:"warning"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK || !body.Warning {
		t.Fatalf("an untrusted certificate must be ok+warning, not a failure: %s", rec.Body.String())
	}
	if !strings.Contains(body.Error, "does not trust its certificate") {
		t.Fatalf("warning text should name the certificate: %q", body.Error)
	}
}

// A failed self-test never reaches handleEchoNonce, so nothing would delete its
// nonce. Minting must sweep, or the map grows without bound.
func TestSelfTestSweepsExpiredNonces(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapLoginAndVerify(t, s)

	s.echoMu.Lock()
	s.echoNonces = map[string]echoNonce{
		"stale-1": {expires: time.Now().Add(-time.Minute)},
		"stale-2": {expires: time.Now().Add(-time.Hour)},
	}
	s.echoMu.Unlock()

	// Nothing is listening on this port, so the probe fails and its own nonce
	// stays — but the two stale entries must be gone.
	doJSON(t, s, http.MethodPost, "/api/v1/admin/public-url/test",
		map[string]string{"url": "http://127.0.0.1:1"}, cookies)

	s.echoMu.Lock()
	defer s.echoMu.Unlock()
	if _, ok := s.echoNonces["stale-1"]; ok {
		t.Fatalf("stale-1 was not swept: %v", s.echoNonces)
	}
	if _, ok := s.echoNonces["stale-2"]; ok {
		t.Fatalf("stale-2 was not swept: %v", s.echoNonces)
	}
}

package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
)

type Config struct {
	PublicURL         string
	PasswordHash      string
	InternalProxyCIDR string
	HTTPClient        *http.Client
}

type Server struct {
	config   Config
	db       *store.DB
	mux      *http.ServeMux
	trusted  *netip.Prefix
	requests *RateLimiter
	failures *RateLimiter
	http     *http.Client
	cacheMu  sync.Mutex
	cimd     map[string]time.Time
}

func NewServer(config Config, db *store.DB) *Server {
	client := config.HTTPClient
	if client == nil {
		client = defaultCIMDClient()
	}
	copyClient := *client
	copyClient.Timeout = 5 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s := &Server{config: config, db: db, mux: http.NewServeMux(), requests: NewRateLimiter(), failures: NewRateLimiter(), http: &copyClient, cimd: map[string]time.Time{}}
	if config.InternalProxyCIDR != "" {
		prefix, err := netip.ParsePrefix(config.InternalProxyCIDR)
		if err != nil {
			panic("invalid LEDGER_INTERNAL_PROXY_CIDR")
		}
		s.trusted = &prefix
	}
	s.mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedMetadata)
	s.mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.protectedMetadata)
	s.mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationMetadata)
	s.mux.HandleFunc("POST /oauth/register", s.register)
	s.mux.HandleFunc("GET /oauth/authorize", s.authorizeGet)
	s.mux.HandleFunc("POST /oauth/authorize", s.authorizePost)
	s.mux.HandleFunc("POST /oauth/token", s.token)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) protectedMetadata(w http.ResponseWriter, _ *http.Request) {
	metadata(w, map[string]any{
		"resource":                 s.config.PublicURL + "/mcp",
		"authorization_servers":    []string{s.config.PublicURL},
		"scopes_supported":         []string{ScopeRead, ScopeWrite},
		"bearer_methods_supported": []string{"header"},
	})
}

func (s *Server) authorizationMetadata(w http.ResponseWriter, _ *http.Request) {
	metadata(w, map[string]any{
		"issuer":                                         s.config.PublicURL,
		"authorization_endpoint":                         s.config.PublicURL + "/oauth/authorize",
		"token_endpoint":                                 s.config.PublicURL + "/oauth/token",
		"registration_endpoint":                          s.config.PublicURL + "/oauth/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               []string{ScopeRead, ScopeWrite},
		"client_id_metadata_document_supported":          true,
		"authorization_response_iss_parameter_supported": true,
	})
}

func metadata(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "max-age=3600")
	_ = json.NewEncoder(w).Encode(value)
}

type registrationRequest struct {
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.requests.Allow("register:"+RealIP(r, s.trusted), 5, time.Minute) {
		w.Header().Set("Retry-After", "60")
		oauthError(w, http.StatusTooManyRequests, "invalid_client", "registration rate limit exceeded")
		return
	}
	var input registrationRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&input); err != nil || len(input.RedirectURIs) == 0 {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "redirect_uris is required")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "request must contain one JSON value")
		return
	}
	for _, redirect := range input.RedirectURIs {
		if !ValidRedirectURI(redirect) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URI must use HTTPS or an allowed loopback host")
			return
		}
	}
	id, err := randomOpaque()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	client, err := s.db.PutClient(r.Context(), store.OAuthClient{ClientID: id, Kind: "dcr", RedirectURIs: input.RedirectURIs, Name: input.ClientName})
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_id": client.ClientID, "client_id_issued_at": client.CreatedAt.Unix(), "client_name": client.Name,
		"redirect_uris": client.RedirectURIs, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"scope": ScopeRead + " " + ScopeWrite,
	})
}

type authorizationRequest struct {
	ClientID, RedirectURI, ResponseType, Challenge, ChallengeMethod, Scope, Resource, State string
}

func authRequest(values url.Values) authorizationRequest {
	return authorizationRequest{
		ClientID: values.Get("client_id"), RedirectURI: values.Get("redirect_uri"), ResponseType: values.Get("response_type"),
		Challenge: values.Get("code_challenge"), ChallengeMethod: values.Get("code_challenge_method"), Scope: values.Get("scope"),
		Resource: values.Get("resource"), State: values.Get("state"),
	}
}

func (s *Server) validateAuthorization(ctx context.Context, request authorizationRequest) (store.OAuthClient, []string, string) {
	client, err := s.resolveClient(ctx, request.ClientID)
	if err != nil {
		return store.OAuthClient{}, nil, "unknown or invalid client"
	}
	if !RedirectMatches(request.RedirectURI, client.RedirectURIs) {
		return store.OAuthClient{}, nil, "unregistered redirect URI"
	}
	if request.ResponseType != "code" {
		return client, nil, "response_type must be code"
	}
	decodedChallenge, err := base64.RawURLEncoding.DecodeString(request.Challenge)
	if request.ChallengeMethod != "S256" || err != nil || len(decodedChallenge) != 32 {
		return client, nil, "S256 PKCE is required"
	}
	scopes, ok := ParseScopes(request.Scope)
	if !ok {
		return client, nil, "invalid scope"
	}
	if request.Resource != "" && request.Resource != s.config.PublicURL+"/mcp" {
		return client, nil, "invalid_target"
	}
	return client, scopes, ""
}

const authorizationStyles = `<style>
:root{color-scheme:light;font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#172033;background:#f7f6f2;font-synthesis:none}*{box-sizing:border-box}body{margin:0;min-height:100vh}.page{width:min(100% - 32px,540px);margin:0 auto;padding:48px 0}.brand{margin-bottom:40px;font-size:22px;font-weight:750;letter-spacing:-.03em}.card{padding:32px;border:1px solid #deddd7;border-radius:18px;background:#fff;box-shadow:0 18px 50px rgba(23,32,51,.08)}.eyebrow{margin:0 0 8px;color:#1769e0;font-size:12px;font-weight:700;letter-spacing:.1em;text-transform:uppercase}h1{margin:0;font-size:28px;line-height:1.2;letter-spacing:-.035em}p{color:#657086;line-height:1.6}.client{margin:24px 0;padding:16px 0;border-block:1px solid #ecebe7}.client strong,.client span{display:block}.client span{margin-top:2px;color:#7a8497;font-size:13px}.permissions{margin:0 0 24px;padding:0;list-style:none}.permissions li{padding:12px 0;border-bottom:1px solid #ecebe7}.permissions strong,.permissions span{display:block}.permissions span{margin-top:3px;color:#657086;font-size:13px;line-height:1.45}form{display:grid;gap:14px}label{display:grid;gap:7px;color:#3d485d;font-size:13px;font-weight:650}input{width:100%;min-height:48px;padding:0 14px;border:1px solid #c9cbd0;border-radius:10px;background:#fff;color:#172033;font:inherit}input:focus{outline:3px solid rgba(23,105,224,.18);border-color:#1769e0}.actions{display:flex;justify-content:flex-end;gap:10px;margin-top:8px}button{min-height:46px;padding:0 18px;border:1px solid #c9cbd0;border-radius:10px;background:#fff;color:#293449;font:inherit;font-weight:700;cursor:pointer}button:hover{background:#f5f5f2}.primary{border-color:#1769e0;background:#1769e0;color:#fff}.primary:hover{background:#125abb}.privacy{margin:18px 0 0;font-size:12px;text-align:center}.error-card p{margin-bottom:0}@media(max-width:520px){.page{padding:24px 0}.brand{margin-bottom:24px}.card{padding:24px 20px;border-radius:15px}.actions{display:grid}.actions button{width:100%}h1{font-size:24px}}
</style>`

var authorizeTemplate = template.Must(template.New("authorize").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Authorize · Ledger</title>` + authorizationStyles + `</head><body><main class="page"><div class="brand">Ledger</div><section class="card" aria-labelledby="authorize-title"><p class="eyebrow">Access request</p><h1 id="authorize-title">Allow {{if .Name}}{{.Name}}{{else}}this app{{end}} to use Ledger?</h1><p>Review what this client will be able to do in your private project memory.</p><div class="client"><strong>{{if .Name}}{{.Name}}{{else}}Unnamed MCP client{{end}}</strong><span>Requested by an MCP client</span></div><ul class="permissions" aria-label="Requested permissions">{{if .Read}}<li><strong>Read project memory</strong><span>View project records and search historical entries.</span></li>{{end}}{{if .Write}}<li><strong>Add and update memory</strong><span>Update project summaries and append permanent timeline entries.</span></li>{{end}}</ul><form method="post" action="/oauth/authorize">{{range $k,$v := .Fields}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}<label for="approval-password">Approval password<input id="approval-password" required type="password" name="password" autocomplete="current-password" autofocus></label><div class="actions"><button name="action" value="deny" formnovalidate>Deny</button><button class="primary" name="action" value="approve">Allow access</button></div></form><p class="privacy">Ledger never shares your password with the requesting client.</p></section></main></body></html>`))
var errorTemplate = template.Must(template.New("error").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Authorization error · Ledger</title>` + authorizationStyles + `</head><body><main class="page"><div class="brand">Ledger</div><section class="card error-card"><p class="eyebrow">Could not continue</p><h1>Authorization error</h1><p>{{.}}</p></section></main></body></html>`))

func (s *Server) authorizeGet(w http.ResponseWriter, r *http.Request) {
	if !s.requests.Allow("authorize:"+RealIP(r, s.trusted), 20, time.Minute) {
		w.Header().Set("Retry-After", "60")
		localErrorStatus(w, http.StatusTooManyRequests, "Too many authorization attempts. Try again in a minute.")
		return
	}
	request := authRequest(r.URL.Query())
	client, scopes, problem := s.validateAuthorization(r.Context(), request)
	if problem != "" {
		localError(w, problem)
		return
	}
	fields := map[string]string{
		"client_id": request.ClientID, "redirect_uri": request.RedirectURI, "response_type": request.ResponseType,
		"code_challenge": request.Challenge, "code_challenge_method": request.ChallengeMethod, "scope": strings.Join(scopes, " "),
		"resource": request.Resource, "state": request.State,
	}
	authorizationPageHeaders(w)
	_ = authorizeTemplate.Execute(w, map[string]any{"Name": client.Name, "Read": HasScope(scopes, ScopeRead), "Write": HasScope(scopes, ScopeWrite), "Fields": fields})
}

func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request) {
	ip := RealIP(r, s.trusted)
	if !s.requests.Allow("authorize:"+ip, 20, time.Minute) {
		w.Header().Set("Retry-After", "60")
		localErrorStatus(w, http.StatusTooManyRequests, "Too many authorization attempts. Try again in a minute.")
		return
	}
	_ = r.ParseForm()
	request := authRequest(r.PostForm)
	client, scopes, problem := s.validateAuthorization(r.Context(), request)
	if problem != "" {
		localError(w, problem)
		return
	}
	if r.PostForm.Get("action") == "deny" {
		redirectAuthorization(w, r, request.RedirectURI, map[string]string{"error": "access_denied", "state": request.State, "iss": s.config.PublicURL})
		return
	}
	if r.PostForm.Get("action") != "approve" {
		localError(w, "invalid action")
		return
	}
	if !VerifyPassword(s.config.PasswordHash, r.PostForm.Get("password")) {
		if !s.failures.Allow("password:"+ip, 4, 15*time.Minute) {
			w.Header().Set("Retry-After", "900")
			localErrorStatus(w, http.StatusTooManyRequests, "Too many failed password attempts. Try again later.")
			return
		}
		localErrorStatus(w, http.StatusUnauthorized, "That approval password was not accepted. Return to the client and try again.")
		return
	}
	code, err := randomOpaque()
	if err != nil || s.db.CreateCode(r.Context(), code, client.ClientID, request.RedirectURI, request.Challenge, scopes) != nil {
		localErrorStatus(w, http.StatusInternalServerError, "Ledger could not complete this authorization. Please try again.")
		return
	}
	redirectAuthorization(w, r, request.RedirectURI, map[string]string{"code": code, "state": request.State, "iss": s.config.PublicURL})
}

func redirectAuthorization(w http.ResponseWriter, r *http.Request, destination string, values map[string]string) {
	u, _ := url.Parse(destination)
	query := u.Query()
	for key, value := range values {
		if value != "" {
			query.Set(key, value)
		}
	}
	u.RawQuery = query.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func localError(w http.ResponseWriter, message string) {
	localErrorStatus(w, http.StatusBadRequest, message)
}

func localErrorStatus(w http.ResponseWriter, status int, message string) {
	authorizationPageHeaders(w)
	w.WriteHeader(status)
	_ = errorTemplate.Execute(w, message)
}

func authorizationPageHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requests.Allow("token:"+RealIP(r, s.trusted), 20, time.Minute) {
		w.Header().Set("Retry-After", "60")
		oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "token rate limit exceeded")
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	if len(r.Header.Values("Authorization")) != 0 {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "token endpoint authentication method is none")
		return
	}
	clientID := r.PostForm.Get("client_id")
	if clientID == "" {
		oauthError(w, http.StatusBadRequest, "invalid_client", "client_id is required")
		return
	}
	var pair store.TokenPair
	var err error
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		pair, err = s.db.ExchangeCode(r.Context(), r.PostForm.Get("code"), clientID, r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"), VerifyPKCE)
	case "refresh_token":
		pair, err = s.db.ExchangeRefresh(r.Context(), r.PostForm.Get("refresh_token"), clientID)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "supported grants are authorization_code and refresh_token")
		return
	}
	if errors.Is(err, store.ErrInvalidGrant) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "grant is invalid, expired, or already used")
		return
	}
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": pair.AccessToken, "token_type": "Bearer", "expires_in": 900,
		"refresh_token": pair.RefreshToken, "scope": strings.Join(pair.Scopes, " "),
	})
}

func supportsTokenAuthNone(preferred string, supported []string) bool {
	if supported == nil {
		return preferred == "" || preferred == "none"
	}

	supportsNone := false
	preferredSupported := preferred == ""
	for _, method := range supported {
		if method == "none" {
			supportsNone = true
		}
		if method == preferred {
			preferredSupported = true
		}
	}
	return supportsNone && preferredSupported
}

func (s *Server) resolveClient(ctx context.Context, id string) (store.OAuthClient, error) {
	client, err := s.db.GetClient(ctx, id)
	if err == nil {
		if client.Kind == "dcr" {
			return client, nil
		}
		s.cacheMu.Lock()
		cached := time.Now().Before(s.cimd[id])
		s.cacheMu.Unlock()
		if cached {
			return client, nil
		}
	}
	u, parseErr := url.Parse(id)
	if parseErr != nil || u.Scheme != "https" || u.Host == "" || u.Path == "" || u.Path == "/" || u.User != nil || u.Fragment != "" {
		if err != nil {
			return store.OAuthClient{}, err
		}
		return store.OAuthClient{}, fmt.Errorf("invalid CIMD client_id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return store.OAuthClient{}, err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return store.OAuthClient{}, fmt.Errorf("CIMD fetch: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return store.OAuthClient{}, fmt.Errorf("CIMD HTTP %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return store.OAuthClient{}, fmt.Errorf("CIMD document too large")
	}
	var document struct {
		ClientID         string   `json:"client_id"`
		RedirectURIs     []string `json:"redirect_uris"`
		ClientName       string   `json:"client_name"`
		TokenAuthMethod  string   `json:"token_endpoint_auth_method"`
		TokenAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := json.Unmarshal(body, &document); err != nil || document.ClientID != id || len(document.RedirectURIs) == 0 || !supportsTokenAuthNone(document.TokenAuthMethod, document.TokenAuthMethods) {
		return store.OAuthClient{}, fmt.Errorf("invalid CIMD metadata")
	}
	for _, redirect := range document.RedirectURIs {
		if !ValidRedirectURI(redirect) {
			return store.OAuthClient{}, fmt.Errorf("invalid CIMD redirect URI")
		}
	}
	client, err = s.db.PutClient(ctx, store.OAuthClient{ClientID: id, Kind: "cimd", RedirectURIs: document.RedirectURIs, Name: document.ClientName})
	if err == nil {
		s.cacheMu.Lock()
		s.cimd[id] = time.Now().Add(24 * time.Hour)
		s.cacheMu.Unlock()
	}
	return client, err
}

var publicIPv6CIMDPrefix = netip.MustParsePrefix("2000::/3")

var reservedCIMDPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
}

func publicCIMDIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Is6() && !publicIPv6CIMDPrefix.Contains(ip) {
		return false
	}
	for _, prefix := range reservedCIMDPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

func defaultCIMDClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("CIMD destination: %w", err)
		}
		var addresses []netip.Addr
		if literal, err := netip.ParseAddr(host); err == nil {
			addresses = []netip.Addr{literal}
		} else {
			addresses, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("CIMD destination lookup: %w", err)
			}
		}
		for _, ip := range addresses {
			if !publicCIMDIP(ip) {
				return nil, fmt.Errorf("CIMD destination is not public: %s", ip)
			}
		}
		var dialErr error
		for _, ip := range addresses {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, fmt.Errorf("CIMD destination: %w", dialErr)
	}
	return &http.Client{Transport: transport}
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": description})
}

func randomOpaque() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

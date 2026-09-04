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

var authorizeTemplate = template.Must(template.New("authorize").Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Authorize Ledger</title></head><body><main><h1>Authorize {{.Name}}</h1><p>Requested scopes: {{.Scope}}</p><form method="post" action="/oauth/authorize">{{range $k,$v := .Fields}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}<label>Password <input required type="password" name="password" autocomplete="current-password"></label><button name="action" value="approve">Approve</button><button name="action" value="deny" formnovalidate>Deny</button></form></main></body></html>`))
var errorTemplate = template.Must(template.New("error").Parse(`<!doctype html><html><body><h1>Authorization error</h1><p>{{.}}</p></body></html>`))

func (s *Server) authorizeGet(w http.ResponseWriter, r *http.Request) {
	if !s.requests.Allow("authorize:"+RealIP(r, s.trusted), 20, time.Minute) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = authorizeTemplate.Execute(w, map[string]any{"Name": client.Name, "Scope": strings.Join(scopes, " "), "Fields": fields})
}

func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request) {
	ip := RealIP(r, s.trusted)
	if !s.requests.Allow("authorize:"+ip, 20, time.Minute) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	_ = r.ParseForm()
	request := authRequest(r.PostForm)
	client, scopes, problem := s.validateAuthorization(r.Context(), request)
	passwordOK := VerifyPassword(s.config.PasswordHash, r.PostForm.Get("password"))
	if !passwordOK {
		if !s.failures.Allow("password:"+ip, 4, 15*time.Minute) {
			w.Header().Set("Retry-After", "900")
			http.Error(w, "too many failed passwords", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}
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
	code, err := randomOpaque()
	if err != nil || s.db.CreateCode(r.Context(), code, client.ClientID, request.RedirectURI, request.Challenge, scopes) != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = errorTemplate.Execute(w, message)
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
		ClientID        string   `json:"client_id"`
		RedirectURIs    []string `json:"redirect_uris"`
		ClientName      string   `json:"client_name"`
		TokenAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(body, &document); err != nil || document.ClientID != id || len(document.RedirectURIs) == 0 || document.TokenAuthMethod != "" && document.TokenAuthMethod != "none" {
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

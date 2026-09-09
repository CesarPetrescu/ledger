package webcors

import "net/http"

const (
	allowMethods  = "GET, POST, DELETE, OPTIONS"
	allowHeaders  = "Authorization, Content-Type, Accept, Last-Event-ID, Mcp-Method, Mcp-Protocol-Version, Mcp-Session-Id"
	exposeHeaders = "Mcp-Session-Id"
)

// AllowExact enables browser access only for the exact public API paths listed.
// It never enables credentialed CORS; Ledger browser clients authenticate with
// OAuth bearer tokens rather than cookies.
func AllowExact(next http.Handler, paths ...string) http.Handler {
	allowed := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		allowed[path] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[r.URL.Path]; !ok {
			next.ServeHTTP(w, r)
			return
		}

		headers := w.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", allowMethods)
		headers.Set("Access-Control-Allow-Headers", allowHeaders)
		headers.Set("Access-Control-Expose-Headers", exposeHeaders)
		headers.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

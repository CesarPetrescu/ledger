package retrieval

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchHTTPRejectsInvalidInput(t *testing.T) {
	handler := NewHTTPHandler(nil, nil)
	for _, body := range []string{`{}`, `{"q":"x","limit":31}`, `{"q":"x"} trailing`} {
		request := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("body %q status = %d", body, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/search", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d", response.Code)
	}
}

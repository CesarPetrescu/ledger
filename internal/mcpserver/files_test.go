package mcpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
)

// A ChatGPT file link may only reach the public internet over HTTPS, so it cannot point Ledger at its
// own host or network.
func TestUploadLinksOnlyReachPublicAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1:443", "10.0.0.5:443", "192.168.10.29:8004", "172.17.0.1:443", "100.64.0.1:443", "169.254.169.254:80", "0.0.0.0:443", "[::1]:443", "[fe80::1]:443", "[fd00::1]:443", "[::ffff:127.0.0.1]:443"} {
		if publicAddressOnly("tcp", address, nil) == nil {
			t.Errorf("%s allowed", address)
		}
	}
	for _, address := range []string{"8.8.8.8:443", "[2606:4700::1111]:443"} {
		if err := publicAddressOnly("tcp", address, nil); err != nil {
			t.Errorf("%s refused: %v", address, err)
		}
	}
	for _, link := range []string{"http://files.example.com/a.csv", "chat_upload://image_0", "https://user:pass@files.example.com/a", ""} {
		if _, err := fetchUpload(context.Background(), chatUpload{DownloadURL: link, FileID: "f"}, 1<<20); err == nil || !strings.Contains(err.Error(), "HTTPS link") {
			t.Errorf("%q: %v", link, err)
		}
	}
	if _, err := fetchUpload(context.Background(), chatUpload{DownloadURL: "https://127.0.0.1:1/a.csv", FileID: "f"}, 1<<20); err == nil || !strings.Contains(err.Error(), "public address") {
		t.Errorf("loopback link: %v", err)
	}
}

// A download stops at its byte budget, whether or not the server announces the size, so a call never
// buffers more than its message may hold.
func TestUploadDownloadsStopAtTheirBudget(t *testing.T) {
	files := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/streamed" {
			w.(http.Flusher).Flush() // no Content-Length
		}
		_, _ = io.WriteString(w, "0123456789")
	}))
	defer files.Close()
	previous := uploadClient
	uploadClient = files.Client()
	defer func() { uploadClient = previous }()
	for _, path := range []string{"/sized", "/streamed"} {
		if _, err := fetchUpload(context.Background(), chatUpload{DownloadURL: files.URL + path, FileID: "f"}, 9); !errors.Is(err, store.ErrHandoffFileLimit) {
			t.Errorf("%s over budget: %v", path, err)
		}
		if file, err := fetchUpload(context.Background(), chatUpload{DownloadURL: files.URL + path, FileID: "f"}, 10); err != nil || string(file.Data) != "0123456789" {
			t.Errorf("%s within budget: %q %v", path, file.Data, err)
		}
	}
}

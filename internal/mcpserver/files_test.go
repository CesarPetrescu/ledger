package mcpserver

import (
	"context"
	"strings"
	"testing"
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
		if _, err := fetchUpload(context.Background(), chatUpload{DownloadURL: link, FileID: "f"}); err == nil || !strings.Contains(err.Error(), "HTTPS link") {
			t.Errorf("%q: %v", link, err)
		}
	}
	if _, err := fetchUpload(context.Background(), chatUpload{DownloadURL: "https://127.0.0.1:1/a.csv", FileID: "f"}); err == nil || !strings.Contains(err.Error(), "public address") {
		t.Errorf("loopback link: %v", err)
	}
}

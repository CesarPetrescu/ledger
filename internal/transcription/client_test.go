package transcription

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func recording() string {
	wav := make([]byte, 44+6400)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 6400)
	return base64.StdEncoding.EncodeToString(wav)
}
func TestSpeechAdapterUsesRealTLSMultipartAndPreservesRomanian(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("missing provider authorization")
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		if _, err := DecodeWAV(base64.StdEncoding.EncodeToString(raw)); err != nil {
			t.Error(err)
		}
		if header.Filename != "capture.wav" || r.FormValue("language") != "ro" || r.FormValue("model") != "fixture" {
			t.Error("wrong transcription request")
		}
		_ = json.NewEncoder(w).Encode(Result{Text: "Ședință Atlas. Verify PostgreSQL."})
	}))
	defer provider.Close()
	client, err := NewClient(provider.URL, "fixture-key", "fixture", provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Transcribe(context.Background(), recording(), "ro")
	if err != nil || result.Text != "Ședință Atlas. Verify PostgreSQL." {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestSpeechRejectsInvalidAudioConfigAndProviderRedirect(t *testing.T) {
	for _, raw := range []string{"", "not-base64", strings.Repeat("a", 1300000)} {
		if _, err := DecodeWAV(raw); err == nil {
			t.Fatal("invalid audio accepted")
		}
	}
	if _, err := NewClient("http://example.com", "", "model", nil); err == nil {
		t.Fatal("cleartext provider accepted")
	}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://other.example.com")
		w.WriteHeader(302)
	}))
	defer provider.Close()
	client, err := NewClient(provider.URL, "fixture-key", "fixture", provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Transcribe(context.Background(), recording(), ""); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("redirect not rejected: %v", err)
	}
}

// Package transcription validates bounded microphone recordings and sends them
// to an operator-configured OpenAI-compatible speech service. Keys stay server-side.
package transcription

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cesarpetrescu/ledger/internal/oauth"
)

const MaxPCMBytes = 30 * 16000 * 2

type Result struct {
	Text string `json:"text"`
}
type Client struct {
	endpoint, key, model string
	http                 *http.Client
}

func NewClient(endpoint, key, model string, transport *http.Client) (*Client, error) {
	if endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || !(u.Scheme == "https" || u.Scheme == "http" && localHost(u.Hostname())) || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("LEDGER_STT_URL must be an HTTPS URL, or HTTP to this machine or a private network, without credentials, query or fragment")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("LEDGER_STT_MODEL is required with LEDGER_STT_URL")
	}
	if len(model) > 200 || strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("invalid speech configuration")
	}
	h := &http.Client{}
	if transport != nil {
		*h = *transport
	} else if u.Scheme == "http" {
		// Cleartext goes straight to the local server, never through an HTTP proxy from the environment.
		direct := http.DefaultTransport.(*http.Transport).Clone()
		direct.Proxy = nil
		h.Transport = direct
	}
	h.Timeout = 25 * time.Second
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{endpoint: endpoint, key: key, model: model, http: h}, nil
}

// localHost says plain HTTP stays off the internet: loopback, a private IP, or a name only a local
// network answers for, such as host.docker.internal for a speech server on the Docker host.
func localHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsLoopback() {
		return true
	}
	return host == "localhost" || oauth.PrivateNetworkHost(host)
}

// Only the canonical RIFF PCM encoding produced by our recorder is accepted.
// This is intentionally not a general-purpose media upload endpoint.
func DecodeWAV(encoded string) ([]byte, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxPCMBytes+44) {
		return nil, errors.New("recording exceeds 30 seconds")
	}
	wav, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(wav) < 44+3200 {
		return nil, errors.New("recording must contain at least 100ms of WAV audio")
	}
	if len(wav) > MaxPCMBytes+44 || len(wav)%2 != 0 {
		return nil, errors.New("invalid recording length")
	}
	u16 := func(n int) uint16 { return binary.LittleEndian.Uint16(wav[n : n+2]) }
	u32 := func(n int) uint32 { return binary.LittleEndian.Uint32(wav[n : n+4]) }
	if string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[12:16]) != "fmt " || string(wav[36:40]) != "data" || u32(4) != uint32(len(wav)-8) || u32(16) != 16 || u16(20) != 1 || u16(22) != 1 || u32(24) != 16000 || u32(28) != 32000 || u16(32) != 2 || u16(34) != 16 || u32(40) != uint32(len(wav)-44) {
		return nil, errors.New("expected 16kHz mono signed-16-bit PCM WAV")
	}
	return wav, nil
}
func (c *Client) Transcribe(ctx context.Context, encoded, language string) (Result, error) {
	if c == nil {
		return Result{}, errors.New("speech service is not configured; type on the phone instead")
	}
	if language != "" && language != "en" && language != "ro" {
		return Result{}, errors.New("language must be en, ro or empty for automatic detection")
	}
	wav, err := DecodeWAV(encoded)
	if err != nil {
		return Result{}, err
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "capture.wav")
	if err != nil {
		return Result{}, err
	}
	if _, err = part.Write(wav); err != nil {
		return Result{}, err
	}
	_ = form.WriteField("model", c.model)
	_ = form.WriteField("response_format", "json")
	if language != "" {
		_ = form.WriteField("language", language)
	}
	if err = form.Close(); err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &body)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Result{}, errors.New("speech service could not be reached")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("speech service returned HTTP %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return Result{}, errors.New("speech response is too large or incomplete")
	}
	var result Result
	if json.Unmarshal(raw, &result) != nil || !utf8.ValidString(result.Text) {
		return Result{}, errors.New("invalid speech response")
	}
	result.Text = strings.TrimSpace(result.Text)
	if n := utf8.RuneCountInString(result.Text); n == 0 || n > 4000 {
		return Result{}, errors.New("transcript must contain 1 to 4000 characters")
	}
	return result, nil
}

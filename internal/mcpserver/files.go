package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Files move between people, agents, and research runs without passing through a model where possible:
//   - agents send small files inline as base64; ChatGPT sends uploads as links (openai/fileParams) that
//     Ledger fetches itself;
//   - agents get short-lived download links (/files/{secret}) to hand to a person;
//   - a research run downloads and uploads over HTTP with its run token (/mcp/research/files).

type publicURLKey struct{}

func withPublicURL(next http.Handler, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), publicURLKey{}, strings.TrimRight(publicURL, "/"))))
	})
}

func publicURLFrom(ctx context.Context) string {
	value, _ := ctx.Value(publicURLKey{}).(string)
	return value
}

// chatUpload is a file ChatGPT passes by reference: a temporary download link. Its schema is the one
// ChatGPT requires for a field listed in _meta["openai/fileParams"].
type chatUpload struct {
	DownloadURL string `json:"download_url" jsonschema:"temporary HTTPS link to the file"`
	FileID      string `json:"file_id" jsonschema:"the file's ID"`
	MimeType    string `json:"mime_type,omitempty" jsonschema:"the file's media type"`
	FileName    string `json:"file_name,omitempty" jsonschema:"the file's name"`
}

// chatFileParams tells ChatGPT that "uploads" takes files the user attached to the conversation.
var chatFileParams = mcp.Meta{"openai/fileParams": []string{"uploads"}}

const uploadsDescription = "files the user attached in ChatGPT; ChatGPT fills these in, and Ledger downloads them (up to 25 MiB each)"
const inlineFilesDescription = "small files as base64, up to 25 MiB in total; each file at most 25 MiB"

// inputFiles turns inline base64 files and ChatGPT uploads into store files.
func inputFiles(ctx context.Context, inline []researchFileInput, uploads []chatUpload) ([]store.ResearchFile, error) {
	if len(inline)+len(uploads) > store.MaxHandoffFiles {
		return nil, store.ErrHandoffFileLimit
	}
	files, err := decodeInlineFiles(inline)
	if err != nil {
		return nil, err
	}
	// Downloads stop as soon as the message's total is reached, so a call never buffers more than that.
	var total int64
	for _, file := range files {
		total += int64(len(file.Data))
	}
	for i, upload := range uploads {
		file, err := fetchUpload(ctx, upload, min(store.MaxHandoffFileBytes, store.MaxHandoffMessageBytes-total))
		if err != nil {
			return nil, fmt.Errorf("uploads[%d]: %w", i, err)
		}
		total += int64(len(file.Data))
		files = append(files, file)
	}
	return files, nil
}

func decodeInlineFiles(inline []researchFileInput) ([]store.ResearchFile, error) {
	files := make([]store.ResearchFile, len(inline))
	var total int
	for i, file := range inline {
		data, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("files[%d].content_base64 must be valid standard base64", i)
		}
		total += len(data)
		files[i] = store.ResearchFile{Filename: file.Filename, MediaType: file.MediaType, Data: data}
	}
	if total > store.MaxResearchSubmitBytes {
		return nil, fmt.Errorf("base64 files are limited to 25 MiB in total; send larger files another way: %w", store.ErrHandoffFileLimit)
	}
	return files, nil
}

// uploadClient fetches ChatGPT's file links. It only reaches public addresses over HTTPS, never a proxy,
// so a link cannot point Ledger at its own network.
var uploadClient = &http.Client{
	Timeout: 2 * time.Minute,
	Transport: &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, Control: publicAddressOnly}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	},
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != "https" {
			return errors.New("file links may only redirect to HTTPS, at most 5 times")
		}
		return nil
	},
}

var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

func publicAddressOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || sharedAddressSpace.Contains(ip) {
		return fmt.Errorf("file links must point to a public address")
	}
	return nil
}

// fetchUpload downloads one ChatGPT upload of at most limit bytes.
func fetchUpload(ctx context.Context, upload chatUpload, limit int64) (store.ResearchFile, error) {
	link, err := url.Parse(upload.DownloadURL)
	if err != nil || link.Scheme != "https" || link.Host == "" || link.User != nil {
		return store.ResearchFile{}, fmt.Errorf("download_url must be an HTTPS link; ChatGPT's mobile apps do not pass files to connectors yet, so attach the file from ChatGPT on the web or in Ledger's console")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return store.ResearchFile{}, err
	}
	response, err := uploadClient.Do(request)
	if err != nil {
		return store.ResearchFile{}, fmt.Errorf("could not download the file: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return store.ResearchFile{}, fmt.Errorf("could not download the file: HTTP %d (the link may have expired)", response.StatusCode)
	}
	if response.ContentLength > limit {
		return store.ResearchFile{}, store.ErrHandoffFileLimit
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return store.ResearchFile{}, fmt.Errorf("could not download the file: %w", err)
	}
	if int64(len(data)) > limit {
		return store.ResearchFile{}, store.ErrHandoffFileLimit
	}
	name := upload.FileName
	if name == "" {
		name = path.Base(link.Path)
	}
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < ' ' {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" {
		name = "upload"
	}
	mediaType := upload.MimeType
	if mediaType == "" {
		mediaType = response.Header.Get("Content-Type")
	}
	if _, _, err := mime.ParseMediaType(mediaType); err != nil {
		mediaType = ""
	}
	return store.ResearchFile{Filename: name, MediaType: mediaType, Data: data}, nil
}

// serveFile sends a stored file as a download. It is never rendered on Ledger's origin, which also
// serves the owner's console.
func serveFile(w http.ResponseWriter, file store.HandoffFile) {
	h := w.Header()
	h.Set("Content-Type", file.MediaType)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Filename}))
	h.Set("Content-Length", strconv.FormatInt(file.SizeBytes, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Data)
}

// fileLinkHandler serves /files/{secret}: a download link an agent handed to a person.
func fileLinkHandler(db *store.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		secret := strings.TrimPrefix(r.URL.Path, "/files/")
		if len(secret) != 43 || strings.Trim(secret, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			http.NotFound(w, r)
			return
		}
		file, err := db.LinkedFile(r.Context(), secret)
		if err != nil {
			if !store.IsNotFound(err) {
				http.Error(w, "could not read the file", http.StatusInternalServerError)
				return
			}
			http.Error(w, "this download link has expired; ask for a new one", http.StatusNotFound)
			return
		}
		serveFile(w, file)
	})
}

// runFiles serves /mcp/research/files to a live run: GET /mcp/research/files/{id} downloads a file of its
// task, and POST /mcp/research/files?filename=NAME uploads the request body for submit's upload_ids.
func runFiles(w http.ResponseWriter, r *http.Request, db *store.DB, run researchRun) {
	writeJSON := func(status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	fail := func(status int, code, message string) {
		writeJSON(status, map[string]string{"error": code, "message": message})
	}
	if raw, ok := strings.CutPrefix(r.URL.Path, "/mcp/research/files/"); ok && r.Method == http.MethodGet {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			fail(http.StatusBadRequest, "invalid_request", "file id must be a positive integer")
			return
		}
		file, err := db.ResearchFile(r.Context(), run.ID, id)
		if store.IsNotFound(err) {
			fail(http.StatusNotFound, "not_found", "no file with that id in this task")
			return
		}
		if err != nil {
			fail(http.StatusInternalServerError, "server_error", "could not read the file")
			return
		}
		serveFile(w, file)
		return
	}
	if r.URL.Path != "/mcp/research/files" || r.Method != http.MethodPost {
		fail(http.StatusNotFound, "not_found", "use GET /mcp/research/files/{id} or POST /mcp/research/files?filename=NAME")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, store.MaxHandoffFileBytes))
	if err != nil {
		fail(http.StatusRequestEntityTooLarge, "too_large", "a file may be at most 25 MiB")
		return
	}
	file, err := db.StageResearchUpload(r.Context(), run.ID, run.Attempt, store.ResearchFile{Filename: r.URL.Query().Get("filename"), MediaType: r.Header.Get("Content-Type"), Data: data})
	switch {
	case errors.Is(err, store.ErrResearchLease) || store.IsNotFound(err):
		fail(http.StatusConflict, "lease_lost", "the run is over")
	case errors.Is(err, store.ErrHandoffFileLimit):
		fail(http.StatusRequestEntityTooLarge, "too_many_files", "a run may upload at most 10 files, 100 MiB in total")
	case err != nil:
		fail(http.StatusBadRequest, "invalid_request", err.Error())
	default:
		writeJSON(http.StatusCreated, map[string]any{"upload_id": strconv.FormatInt(file.ID, 10), "filename": file.Filename, "media_type": file.MediaType, "size_bytes": file.SizeBytes, "sha256": file.SHA256})
	}
}

// eachFile visits every file in a research context: the brief's and the thread's.
func (o *researchContextOutput) eachFile(visit func(*researchFileOutput)) {
	for i := range o.Files {
		visit(&o.Files[i])
	}
	for i := range o.Thread {
		for j := range o.Thread[i].Files {
			visit(&o.Thread[i].Files[j])
		}
	}
}

//go:build integration

package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/cesarpetrescu/ledger/internal/testdb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func download(t *testing.T, link, token string) (int, http.Header, string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, link, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, response.Header, string(body)
}

// The whole file path of a research task: a chat agent sends the user's files (inline and as a ChatGPT
// upload link), the run downloads them over HTTP, uploads its result file and submits it, and the agent
// gets a download link for the user. Review feedback can carry files to the next run.
func TestResearchFilesTravelEndToEnd(t *testing.T) {
	db, ctx := testdb.Open(t)
	addAccess(t, db, ctx, "agent-token", []string{"ledger:read", "ledger:write"})
	var handler http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	defer server.Close()
	handler = HTTPHandler(NewServer(db, "http://unused"), db, server.URL)

	// ChatGPT hosts the user's upload behind a temporary HTTPS link.
	chatFiles := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/file-abc" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, "engine,tokens_per_s\nvllm,120\n")
	}))
	defer chatFiles.Close()
	previous := uploadClient
	uploadClient = chatFiles.Client()
	defer func() { uploadClient = previous }()

	agent := connectMCP(t, server.URL+"/mcp", "agent-token", "chatgpt")
	tools, err := agent.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, tool := range tools.Tools {
		if tool.Name != "create_research_task" && tool.Name != "review_research_task" {
			continue
		}
		checked++
		params, _ := json.Marshal(tool.Meta["openai/fileParams"])
		schema, _ := json.Marshal(tool.InputSchema)
		var shape struct {
			Properties struct {
				Uploads struct {
					Items struct {
						Properties map[string]any `json:"properties"`
						Required   []string       `json:"required"`
					} `json:"items"`
				} `json:"uploads"`
			} `json:"properties"`
		}
		_ = json.Unmarshal(schema, &shape)
		items := shape.Properties.Uploads.Items
		if string(params) != `["uploads"]` || len(items.Properties) != 4 || strings.Join(items.Required, ",") != "download_url,file_id" {
			t.Errorf("%s file params = %s, uploads items = %#v", tool.Name, params, items)
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d file-taking tools", checked)
	}

	task := callTool[researchTaskOutput](t, agent, "create_research_task", map[string]any{
		"title": "Benchmark engines", "objective": "Rerun the benchmark in the attached data", "acceptance": []string{"Return a CSV"},
		"files":   []map[string]any{{"filename": "notes.txt", "media_type": "text/plain", "content_base64": base64.StdEncoding.EncodeToString([]byte("use an 8 GB GPU"))}},
		"uploads": []map[string]any{{"download_url": chatFiles.URL + "/file-abc", "file_id": "file-abc", "file_name": "bench.csv"}},
	})
	id, _ := strconv.ParseInt(task.ID, 10, 64)

	// The run downloads the brief's files to disk with its token.
	claim, err := db.ClaimResearchTask(ctx, 60, "Adastrion", "dispatcher")
	if err != nil || claim == nil || claim.Task.ID != id {
		t.Fatalf("claim = %#v, %v", claim, err)
	}
	run := connectMCP(t, server.URL+"/mcp/research", claim.Token, "adastrion-run")
	brief := callTool[researchContextOutput](t, run, "get_task", map[string]any{})
	got := map[string]researchFileOutput{}
	for _, f := range brief.Files {
		got[f.Filename] = f
	}
	bench, notes := got["bench.csv"], got["notes.txt"]
	if len(brief.Files) != 2 || bench.MediaType != "text/csv" || bench.DownloadURL != server.URL+"/mcp/research/files/"+bench.ID || bench.DownloadExpiresAt != nil || notes.ID == "" {
		t.Fatalf("brief files = %#v", brief.Files)
	}
	if status, _, body := download(t, bench.DownloadURL, claim.Token); status != 200 || body != "engine,tokens_per_s\nvllm,120\n" {
		t.Fatalf("run download = %d %q", status, body)
	}
	if status, _, _ := download(t, bench.DownloadURL, ""); status != http.StatusUnauthorized {
		t.Fatalf("download without the run token = %d", status)
	}

	// It uploads its result file over HTTP, plus one it then leaves out, and submits by upload ID.
	upload := func(name, content string) string {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/mcp/research/files?filename="+name, strings.NewReader(content))
		request.Header.Set("Authorization", "Bearer "+claim.Token)
		request.Header.Set("Content-Type", "text/csv")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(response.Body).Decode(&out)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("upload %s = %d %v", name, response.StatusCode, out)
		}
		return out["upload_id"].(string)
	}
	result := upload("result.csv", "engine,tokens_per_s\nvllm,131\nllama.cpp,58\n")
	upload("scratch.csv", "unused")
	if bad, err := run.CallTool(ctx, &mcp.CallToolParams{Name: "submit", Arguments: map[string]any{"deliverable": "Done", "upload_ids": []string{"999999"}}}); err != nil || !bad.IsError {
		t.Fatalf("submit with an unknown upload = %#v, %v", bad, err)
	}
	callTool[submitOutput](t, run, "submit", map[string]any{"deliverable": "vLLM is faster; see result.csv", "upload_ids": []string{result}})
	var left int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM research_upload WHERE handoff_id=$1`, id).Scan(&left); err != nil || left != 0 {
		t.Fatalf("uploads left after submit = %d, %v", left, err)
	}

	// The agent gets a link the user can open without credentials, as a download, for an hour.
	view := callTool[researchView](t, agent, "get_research_task", map[string]any{"id": task.ID})
	var resultFile researchFileOutput
	view.eachFile(func(f *researchFileOutput) {
		if f.Filename == "result.csv" {
			resultFile = *f
		}
	})
	if view.Result != "awaiting_review" || !strings.HasPrefix(resultFile.DownloadURL, server.URL+"/files/") || resultFile.DownloadExpiresAt == nil {
		t.Fatalf("view = %s, result file %#v", view.Result, resultFile)
	}
	status, header, body := download(t, resultFile.DownloadURL, "")
	if status != 200 || body != "engine,tokens_per_s\nvllm,131\nllama.cpp,58\n" || !strings.HasPrefix(header.Get("Content-Disposition"), "attachment") ||
		header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("link download = %d %q %v", status, body, header)
	}
	if status, _, _ := download(t, server.URL+"/files/"+strings.Repeat("A", 43), ""); status != http.StatusNotFound {
		t.Fatalf("unknown link = %d", status)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE file_link SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := download(t, resultFile.DownloadURL, ""); status != http.StatusNotFound {
		t.Fatalf("expired link = %d", status)
	}
	if err := db.SweepResearchFiles(ctx); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM file_link`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("links after sweep = %d, %v", links, err)
	}

	// Send-back feedback carries a file to the next run.
	callTool[researchTaskOutput](t, agent, "review_research_task", map[string]any{"id": task.ID, "action": "send_back", "feedback": "Add AMD numbers from the attached sheet.",
		"files": []map[string]any{{"filename": "amd.csv", "content_base64": base64.StdEncoding.EncodeToString([]byte("gpu,vram\n7900xtx,24\n"))}}})
	next, err := db.ClaimResearchTask(ctx, 60, "Adastrion", "dispatcher")
	if err != nil || next == nil || next.Reason != "revision" {
		t.Fatalf("revision claim = %#v, %v", next, err)
	}
	second := connectMCP(t, server.URL+"/mcp/research", next.Token, "adastrion-run")
	pack := callTool[researchContextOutput](t, second, "get_task", map[string]any{})
	var amd researchFileOutput
	pack.eachFile(func(f *researchFileOutput) {
		if f.Filename == "amd.csv" {
			amd = *f
		}
	})
	if status, _, body := download(t, amd.DownloadURL, next.Token); status != 200 || body != "gpu,vram\n7900xtx,24\n" {
		t.Fatalf("feedback file = %#v: %d %q", amd, status, body)
	}
	// The first run's token no longer reaches files.
	if status, _, _ := download(t, bench.DownloadURL, claim.Token); status != http.StatusUnauthorized {
		t.Fatalf("ended run's download = %d", status)
	}
	if _, err := db.ReviewResearch(ctx, id, "send_back", "", "c", "c", []store.ResearchFile{{Filename: "x", Data: []byte("x")}}); err == nil {
		t.Fatal("files without feedback accepted")
	}
}

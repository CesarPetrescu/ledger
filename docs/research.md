# Research tasks

Ledger is the control plane and the memory for sandboxed research. It holds each task's spec, its lease, the run's checkpoint, the result, and the owner's review. Ledger does not run anything itself. A dispatcher, outside Ledger, claims tasks and starts one sandbox per run. The agent in that sandbox never sees the queue. It gets one task and a token that works only for that task, and only while that run holds the lease.

```text
agent (/mcp) ──create_research_task──▶ Ledger ◀──claim / renew / end── dispatcher (/api/v1 or /mcp/dispatch)
                                         ▲                                   │ starts a sandbox with
owner (console) ──review: accept,        │                                   ▼ LEDGER_RESEARCH_URL + token
   send back, answer, retry──────────────┘◀──get_task, heartbeat, checkpoint, submit, ask_owner── run (/mcp/research)
```

## The task

A research task is a handoff of kind `research`. Its first message, the brief, shows the spec as Markdown. The brief's work state is the task's state:

| Brief state | Phase | Meaning | Owner's buttons |
|---|---|---|---|
| `draft` | | Not queued yet | Queue |
| `ready` | | Queued; claimable once every `depends_on` task is accepted | |
| `in_progress` | | Leased to a run | Stop and requeue |
| `blocked` | `review` | The run submitted a result | Accept, Send back |
| `blocked` | `question` | The run asked the owner something | Resume |
| `blocked` | `dead` | `max_attempts` runs failed | Retry (resets the failure count) |
| `done` | | Accepted | Run again |

Every other message in the thread is a note: run started, run failed, the deliverable, the question, and the owner's replies. To send a result back or answer a question, add a reply, then press Send back or Resume. The next run receives the thread through `get_task`.

The spec has these fields:

- `objective`: up to 8000 characters.
- `acceptance`: 1 to 20 one-line checks.
- `deliverable`: `report` (the default), `answer`, `dataset`, or `code`.
- `eval_cmd`: optional.
- `budget`: `rounds`, `minutes`, and `tokens`, where 0 means no limit.

The task also has `max_attempts` (1 to 10, default 3) and `depends_on`. Ledger stores and returns the budget, and the dispatcher enforces it.

Research threads are owner-only. `list_handoffs`, `get_handoff`, and the other handoff tools on `/mcp` do not return them, so unreviewed web-derived text never reaches the owner's other agents.

## Leases and attempts

- `claim_research_task` leases the oldest ready task for `lease_seconds` (30 to 3600, default 300), increments `attempt`, and returns a new run token.
- The lease is renewed by the dispatcher (`renew_research_lease`) and by the run itself (`heartbeat`, `checkpoint`).
- A run ends one of three ways:
  - **`submit`**: phase `review`.
  - **`ask_owner`**: phase `question`.
  - **Failure**: the lease lapses, or the dispatcher calls `end_research_run` before a submit. A failure adds one to `failures` and sets `last_error`. The task goes back to `ready`, or becomes `dead` once `failures` reaches `max_attempts`.
  - **Stopped by the owner** (Stop and requeue): the task goes back to `ready` without counting a failure, and the thread notes the stop. The dispatcher's renewal then fails with `lease_lost`.
- Ledger checks for lapsed leases every 15 seconds, so a task returns to the queue within the lease time plus 15 seconds even if the dispatcher has died.
- The run token works only while its task is `in_progress` under the same attempt with a live lease. It stops working the moment the run ends.
- `checkpoint` stores up to 64 KiB of state. The next run gets it from `get_task` (`checkpoint` and `checkpoint_attempt`) and should resume from it.

## Dispatcher API: `/api/v1` (API keys)

This is the simplest way for a server, such as Adastrion Core, to run research. It's plain JSON over HTTPS, with no OAuth. In the console, create a key under **Agents › API keys**. Ledger shows the key once and stores only its hash. A key can only dispatch research. Send it as `Authorization: Bearer <key>`.

A dispatcher loop:

1. Respect your concurrency limit. Then claim, waiting up to 25 seconds for a task:

   ```sh
   curl -s -X POST https://ledger.example.com/api/v1/research/claim \
     -H "Authorization: Bearer $LEDGER_API_KEY" -H 'Content-Type: application/json' \
     -d '{"wait_seconds": 25, "lease_seconds": 300}'
   ```

   `204 No Content` means the queue stayed empty, so claim again. `200` returns the run:

   ```json
   {
     "claimed": true,
     "task": {"id": "12", "title": "Vector DB survey", "project_slug": "atlas", "attempt": 2, "spec": {"objective": "…", "acceptance": ["…"], "deliverable": "report", "budget": {"minutes": 30}}},
     "token": "…run token…",
     "endpoint": "https://ledger.example.com/mcp/research",
     "reason": "revision",
     "available": {"checkpoint_attempt": 1, "notes": 3, "owner_notes": 1, "files": 2, "project_name": "Atlas", "project_shared": true},
     "chat": {"title": "Research #12 · Vector DB survey (run 2, revision)", "opening": "You are running Ledger research task #12, …"}
   }
   ```

2. Open a chat titled `chat.title`, with `chat.opening` as its first message. Add an MCP server `endpoint` with header `Authorization: Bearer <token>` to that chat's agent. The opening already says why this run exists (`reason`) and which research tool holds what, so the agent knows where to look.
3. Every `lease_seconds / 3`, renew:

   ```sh
   curl -s -X POST https://ledger.example.com/api/v1/research/tasks/12/renew \
     -H "Authorization: Bearer $LEDGER_API_KEY" -d '{"attempt": 2}'
   ```

   `409 lease_lost` means the run is over: it submitted, asked you, lapsed, or you stopped it. Close the chat's MCP session.
4. When the chat ends, report it:

   ```sh
   curl -s -X POST https://ledger.example.com/api/v1/research/tasks/12/end \
     -H "Authorization: Bearer $LEDGER_API_KEY" -d '{"attempt": 2, "error": "chat closed without submitting"}'
   ```

   A run that never submitted or asked counts as a failure and is retried.

| Call | Purpose |
|---|---|
| `GET /api/v1/ping` | Checks the key; returns its name and scopes |
| `POST /api/v1/research/claim` | `{"wait_seconds": 0-25, "lease_seconds": 30-3600}`; returns `200` with the run as above, or `204` |
| `POST /api/v1/research/tasks/{id}/renew` | `{"attempt": n}`; returns `{"lease_until"}`, or `409 lease_lost` |
| `POST /api/v1/research/tasks/{id}/end` | `{"attempt": n, "error": "…"}`; returns the task |
| `GET /api/v1/research/tasks?status=&limit=` | Overview. `status` is `queued`, `running`, `review`, `question`, `stopped`, `accepted`, `draft`, or `all`; the default is everything in flight |
| `GET /api/v1/research/tasks/{id}` | One task with its spec, counters, progress, and checkpoint |

`reason` is one of:
- `first_run`;
- `retry_after_failure`: the last run failed, and its error is in the opening;
- `revision`: you sent the result back;
- `answered`: you answered its question;
- `retry`: you restarted a stopped task;
- `restarted`: you stopped a running run;
- `reopened`: you reopened an accepted task.

Errors are JSON, `{"error": "...", "message": "..."}`:
- 401: the key is missing, wrong, or revoked;
- 400: bad input;
- 404: unknown task;
- 403: another dispatcher's run.

API keys don't work on `/mcp`, and agents' OAuth tokens don't work on `/api`.

## Dispatcher over MCP: `/mcp/dispatch` (OAuth)

This is the same dispatching as `/api/v1`, for MCP clients that sign in with OAuth. Its claim returns the same fields, `chat` included. The dispatcher authenticates with OAuth and the `research:dispatch` scope. Ledger never advertises that scope and never grants it by default, so only a client that asks for it by name, and that you approve, gets it. One way to set it up:

1. Register a device client at `POST /oauth/register` with `grant_types: ["urn:ietf:params:oauth:grant-type:device_code", "refresh_token"]` and a recognisable `client_name`.
2. Start the device flow at `POST /oauth/device` with `scope=research:dispatch`, then approve the code in the Ledger console.
3. Poll `POST /oauth/token` for the access and refresh tokens, and refresh the access token before it expires.

Then use these MCP tools, with `clientInfo.name` set to the dispatcher's name; claims are attributed to that name:

| Tool | Input | Output |
|---|---|---|
| `claim_research_task` | `lease_seconds?` | `claimed`, `task`, `token`, `endpoint` |
| `renew_research_lease` | `task_id`, `attempt` | `lease_until`, or the error `lease_lost`: stop the sandbox |
| `end_research_run` | `task_id`, `attempt`, `error?` | the task. Call it on every sandbox exit; it counts a failure only if the run had not submitted or asked |

A loop that fits this contract:

1. Respect your concurrency cap and time window.
2. Call `claim_research_task`.
3. Start a sandbox with `LEDGER_RESEARCH_URL=<endpoint>` and `LEDGER_RESEARCH_TOKEN=<token>`, and no other secrets.
4. Renew the lease every `lease_seconds / 3`.
5. When the sandbox exits, call `end_research_run`.

A 401 or 403 from `/mcp/dispatch` means the dispatcher's own token is missing, expired, or lacks the scope.

## Run: `/mcp/research`

The run connects with `Authorization: Bearer $LEDGER_RESEARCH_TOKEN`. This endpoint has exactly these tools. None of them takes a task ID, so a run cannot address any other task:

| Tool | What it does |
|---|---|
| `get_task` | Returns the spec, attempt counters, last checkpoint, the project summary (only if the owner turned on **Share with research runs** for that project), and this task's thread, newest 30 messages first. Pass `before=next_before` for older ones |
| `read_file` | Reads a file attached to this task's brief or thread, such as an owner's attachment or an earlier run's deliverable files |
| `heartbeat` | Renews the lease, with an optional one-line `progress` |
| `checkpoint` | Saves compact resumable `state` and renews the lease |
| `submit` | Sends the Markdown `deliverable` and up to 10 `files` (25 MiB in total, base64) for review. Ends the run |
| `ask_owner` | Asks the owner a `question`. Ends the run |

After the run ends, the token gets 401 on every endpoint. `/mcp` and `/mcp/dispatch` always reject run tokens, and `/mcp/research` rejects OAuth tokens.

## Sandbox network

The sandbox's only secret is its run token, and everything it writes lands in an owner-only thread. Give it open egress for research, but no route to private networks, the host, or other sandboxes. It should reach Ledger only through its public URL, at `/mcp/research`. Adastrion Core's sandbox router already enforces this shape.

### Continue an accepted task

`create_research_task` accepts optional `continue_from_task_id`, a positive ID string.
The predecessor must already be accepted and must have the same `project_slug` (including no project).
Creation freezes its accepted attempt in `continue_from_attempt` and copies only that run's submission
and attachments into the new task. The inherited note identifies its source. Private owner notes and
rejected submissions are excluded. `get_task` and `read_file` work on the new task's own copies, with
new file IDs; a run still cannot read arbitrary predecessor files. Reopening the predecessor later
does not change the copied result.

An API-key dispatcher may also request a follow-up from an accepted task:

```http
POST /api/v1/research/tasks/12/continue
Authorization: Bearer <dispatcher-api-key>
Content-Type: application/json

{"title":"Refresh the survey","objective":"Check new evidence since the accepted survey","acceptance":["Cite changed findings"]}
```

Returns 201 and the queued task. The project, deliverable, budget and maximum failed attempts are
inherited; the old evaluation command is not. Missing tasks return 404, unaccepted tasks 409, invalid
input 400, and invalid keys 401. This narrowly extends `research:dispatch` to request follow-up work;
it does not grant arbitrary task creation or owner review. Run tokens cannot use the endpoint.
Dispatchers can use the lineage pair to restore a matching retained workspace without guessing topics.

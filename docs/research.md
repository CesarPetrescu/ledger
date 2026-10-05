# Research tasks

Ledger is the control plane and the memory for sandboxed research. It holds each task's spec, its lease, the run's checkpoint, the result, and the owner's review. Ledger does not run anything itself. A dispatcher, outside Ledger, claims tasks and starts one sandbox per run. The agent in that sandbox never sees the queue. It gets one task and a token that works only for that task, and only while that run holds the lease.

```text
agent (/mcp) ──create_research_task──▶ Ledger ◀──claim / renew / end── dispatcher (/mcp/dispatch)
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
  - **Failure**: the lease lapses, the dispatcher calls `end_research_run` before a submit, or the owner stops it. A failure adds one to `failures` and sets `last_error`. The task goes back to `ready`, or becomes `dead` once `failures` reaches `max_attempts`.
- Ledger checks for lapsed leases every 15 seconds, so a task returns to the queue within the lease time plus 15 seconds even if the dispatcher has died.
- The run token works only while its task is `in_progress` under the same attempt with a live lease. It stops working the moment the run ends.
- `checkpoint` stores up to 64 KiB of state. The next run gets it from `get_task` (`checkpoint` and `checkpoint_attempt`) and should resume from it.

## Dispatcher: `/mcp/dispatch`

The dispatcher authenticates with OAuth and the `research:dispatch` scope. Ledger never advertises that scope and never grants it by default, so only a client that asks for it by name, and that you approve, gets it. One way to set it up:

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
| `get_task` | Returns the spec, attempt counters, last checkpoint, the project summary (only if the owner turned on **Share with research runs** for that project), and this task's thread |
| `heartbeat` | Renews the lease, with an optional one-line `progress` |
| `checkpoint` | Saves compact resumable `state` and renews the lease |
| `submit` | Sends the Markdown `deliverable` and up to 10 `files` (25 MiB in total, base64) for review. Ends the run |
| `ask_owner` | Asks the owner a `question`. Ends the run |

After the run ends, the token gets 401 on every endpoint. `/mcp` and `/mcp/dispatch` always reject run tokens, and `/mcp/research` rejects OAuth tokens.

## Sandbox network

The sandbox's only secret is its run token, and everything it writes lands in an owner-only thread. Give it open egress for research, but no route to private networks, the host, or other sandboxes. It should reach Ledger only through its public URL, at `/mcp/research`. Adastrion Core's sandbox router already enforces this shape.

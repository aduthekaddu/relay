# Agents list

The Agents area lists coding agents installed on this machine. An installed row names a binary that is actually on disk. The recipe does not launch an agent.

## Sub-features

- `agents-list`. `GET /api/v1/agents` returns the catalog.
- `agents-binary`. One row with `installed` true names a `binary` that exists and is executable.

## How to get to it (user POV)

- In the web UI, the rail link "Agents" (`a[href="/agents"]` inside `nav[aria-label="Areas"]`) opens the area.
- Over the API, `GET /api/v1/agents` returns a JSON array of agents.

## Driving it with control-relay

Preconditions:

- `ctl doctor` passes for a run started by `ctl up`.
- At least one agent binary is installed. If every row has `installed` false, stop and report that prerequisite.

- **List agents.** The user opens Agents. Run `ctl term run --out /tmp/rv-agents.json -- python3 /tmp/rv-agents.py`. The output is a JSON array. Pick one object whose `installed` field is true and whose `binary` field is a path.
- **Read it back.** The user could run that binary. Run `ctl term run --out /tmp/rv-agents-bin.json -- /bin/sh -c 'test -x "$1"' _ <binary>`. The exit is 0. The placeholder `<binary>` is the path from the list, not a guess.
- **Proof.** Record each step with `ctl evidence add`. Mark the `test -x` step with `--readback`.

`/tmp/rv-agents.py` sends `GET /api/v1/agents` over the unix socket (`HTTP/1.0`, host `relay`) and prints the response body.

## Gotchas

- Do not launch an agent, and do not install hooks. The isolated config sets `hooks` false and `index_history` false so this run does not scan the account home.
- `ctl api` over TCP is not signed in. Use the control socket.
- A binary path from the response is the value to test. Do not substitute a binary you found some other way and still call the list verified.

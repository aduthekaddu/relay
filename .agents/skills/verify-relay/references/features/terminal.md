# Terminal survives a serve restart

A shell the user opened is still there after the owned web server process restarts. The screen still shows what they typed. Docs state this in `docs/dev/ARCHITECTURE.md` and `docs/dev/PTYD.md`.

## Sub-features

- `terminal-create`. Open a shell session and see a marker on its screen.
- `terminal-survive`. After `ctl restart`, the same session id still shows that marker and is not exited.

## How to get to it (user POV)

- In the web UI, the rail link "Terminal" (`a[href="/terminal"]` inside `nav[aria-label="Areas"]`) opens the area. A session opens at `/terminal/<id>`.
- Over the API, `POST /api/v1/terminals` with `{"name":"verify","command":["/bin/sh"]}` creates the session. `POST /api/v1/terminals/<id>/input` with `{"data":"<bytes>"}` types into it. `GET /api/v1/terminals/<id>/snapshot` returns the screen text.

## Driving it with control-relay

Preconditions:

- `ctl doctor` passes for a run started by `ctl up`.
- The process can open a pty slave. If `POST /api/v1/terminals` returns a message containing `operation not permitted`, stop. Report the entry point and that prerequisite. Do not attach this failure to a bundle for a different feature.

Write the programs below to `/tmp` (outside the repo) and run them with `ctl term run`, which has `RELAY_SOCKET` set. `ctl api` is not signed in.

- **Create a shell.** The user opens Terminal and starts a shell. Run `ctl term run --out /tmp/rv-term-create.json -- python3 /tmp/rv-term-create.py`. The output contains `"id": "t_` and an `activity` other than `exited`. Save the id.
- **Type a marker.** The user runs a print. Run `ctl term run --out /tmp/rv-term-input.json -- python3 /tmp/rv-term-input.py`. Then poll `python3 /tmp/rv-term-snap.py` until the output contains `marker-term-1`. Record that snapshot.
- **Read it back after restart.** The user restarts only the web server. Run `ctl restart`, then `python3 /tmp/rv-term-snap.py` again. Run `ctl term run --out /tmp/rv-term-after.json -- python3 /tmp/rv-term-snap.py`. The same id's screen still contains `marker-term-1`, and `GET /api/v1/terminals/<id>` does not report `"activity":"exited"`.
- **Proof.** Record each step with `ctl evidence add`. Mark the post-restart snapshot with `--readback`. Pass `--exit` from the term JSON and `--expect-exit 0`.

`/tmp/rv-term-create.py` writes the session id to `/tmp/rv-term-id` and prints the response JSON:

```python
import json, os, socket
body = json.dumps({"name": "verify", "command": ["/bin/sh"]}).encode()
req = (
    "POST /api/v1/terminals HTTP/1.0\r\nHost: relay\r\n"
    "Content-Type: application/json\r\nContent-Length: %d\r\n\r\n" % len(body)
).encode() + body
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(os.environ["RELAY_SOCKET"])
s.sendall(req)
data = b""
while True:
    chunk = s.recv(65536)
    if not chunk:
        break
    data += chunk
raw, _, payload = data.partition(b"\r\n\r\n")
status = int(raw.split()[1])
print(payload.decode())
if status == 201 or status == 200:
    open("/tmp/rv-term-id", "w").write(json.loads(payload.decode())["id"])
raise SystemExit(0 if status < 300 else 1)
```

`/tmp/rv-term-input.py` types the marker. The carriage return is the byte the pty needs:

```python
import json, os, socket
sid = open("/tmp/rv-term-id").read().strip()
body = json.dumps({"data": "printf '%s\\n' marker-term-1\r"}).encode()
req = (
    "POST /api/v1/terminals/%s/input HTTP/1.0\r\nHost: relay\r\n"
    "Content-Type: application/json\r\nContent-Length: %d\r\n\r\n" % (sid, len(body))
).encode() + body
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(os.environ["RELAY_SOCKET"])
s.sendall(req)
data = b""
while True:
    chunk = s.recv(65536)
    if not chunk:
        break
    data += chunk
raw, _, _ = data.partition(b"\r\n\r\n")
raise SystemExit(0 if int(raw.split()[1]) < 300 else 1)
```

`/tmp/rv-term-snap.py` prints the screen text. Its source has no marker:

```python
import json, os, socket
sid = open("/tmp/rv-term-id").read().strip()
req = ("GET /api/v1/terminals/%s/snapshot?lines=50 HTTP/1.0\r\nHost: relay\r\n\r\n" % sid).encode()
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(os.environ["RELAY_SOCKET"])
s.sendall(req)
data = b""
while True:
    chunk = s.recv(65536)
    if not chunk:
        break
    data += chunk
raw, _, payload = data.partition(b"\r\n\r\n")
doc = json.loads(payload.decode())
print(doc.get("text", ""), end="")
raise SystemExit(0 if int(raw.split()[1]) == 200 else 1)
```

## Gotchas

- A sandbox that cannot open `/dev/ttys*` fails at create, on a good build and on a broken one. That result does not falsify the claim.
- The commit before `c35acbf` is the wrong bad state. That change stops a managed unit from starting a second ptyd. A direct `relay serve` already detached ptyd before it. The break that falsifies this recipe is removing the setsid on the ptyd spawn, in a throwaway worktree only, then restarting by signalling the serve process group.
- `ctl restart` must leave ptyd running. `down` re-reads `run/ptyd.lock` so it stops the daemon that is actually live.
- Do not point `RELAY_SOCKET` at a real instance. Unset `RELAY_SOCKET`, `RELAY_SESSION` and `RELAY_CONFIG` before `up`.

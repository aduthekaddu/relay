# Files save and read back

Saving a text file from the files area writes those bytes. A later read, from the API and from disk, returns the same bytes.

## Sub-features

- `files-save`. `PUT /api/v1/files/text` creates `note.txt` in the scratch folder and reports its size.
- `files-read`. `GET /api/v1/files/text` and a direct read of the file both return the saved text.

## How to get to it (user POV)

- In the web UI, the rail link "Files" (`a[href="/files"]` inside `nav[aria-label="Areas"]`) opens the area. The user opens a text file and saves it.
- Over the API, `PUT /api/v1/files/text?path=<absolute>` with body `{"text":"<bytes>"}` saves, and `GET` on the same path reads it back.

## Driving it with control-relay

Preconditions:

- `ctl doctor` passes for a run started by `ctl up`.
- `up` has set the files root to the physical path of `$RELAY_HOME/scratch`.

Write the two programs to `/tmp/rv-files-put.py` and `/tmp/rv-files-get.py`, outside the repo. Run them with `ctl term run` so `RELAY_SOCKET` is set. Keep the marker string out of the GET program and out of the disk command.

- **Save a text file.** The user saves `note.txt`. Run `ctl term run --out /tmp/rv-files-put.json -- python3 /tmp/rv-files-put.py`. The output contains `wrote-bytes=15` and `name=note.txt`, and the exit is 0.
- **Read it back through the API.** The user opens the file again. Run `ctl term run --out /tmp/rv-files-get.json -- python3 /tmp/rv-files-get.py`. The output contains `marker-files-1`.
- **Read it back from disk.** Run `ctl term run --out /tmp/rv-files-disk.json -- /bin/sh -c 'cat "$RELAY_HOME/scratch/note.txt"'`. The output contains `marker-files-1`.
- **Proof.** Record each step with `ctl evidence add`. Mark the disk read with `--readback`. Pass `--exit` from the term JSON and `--expect-exit 0`. The save step's `--expect-contains` is `wrote-bytes=15`. The two reads use `marker-files-1`.

`/tmp/rv-files-put.py`:

```python
import json, os, socket, subprocess, urllib.parse
home = os.environ["RELAY_HOME"]
scratch = subprocess.check_output(
    ["/bin/sh", "-c", 'cd "$1/scratch" && pwd -P', "sh", home], text=True
).strip()
path = scratch + "/note.txt"
body = json.dumps({"text": "marker-files-1\n"}).encode()
qs = urllib.parse.urlencode({"path": path})
head = (
    "PUT /api/v1/files/text?%s HTTP/1.0\r\nHost: relay\r\n"
    "Content-Type: application/json\r\nContent-Length: %d\r\n\r\n"
    % (qs, len(body))
).encode()
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(os.environ["RELAY_SOCKET"])
s.sendall(head + body)
data = b""
while True:
    chunk = s.recv(65536)
    if not chunk:
        break
    data += chunk
raw, _, payload = data.partition(b"\r\n\r\n")
status = int(raw.split()[1])
doc = json.loads(payload.decode())
print("wrote-bytes=" + str(doc.get("size")))
print("name=" + str(doc.get("name")))
raise SystemExit(0 if status == 200 else 1)
```

`/tmp/rv-files-get.py`:

```python
import json, os, socket, subprocess, urllib.parse
home = os.environ["RELAY_HOME"]
scratch = subprocess.check_output(
    ["/bin/sh", "-c", 'cd "$1/scratch" && pwd -P', "sh", home], text=True
).strip()
qs = urllib.parse.urlencode({"path": scratch + "/note.txt"})
req = ("GET /api/v1/files/text?%s HTTP/1.0\r\nHost: relay\r\n\r\n" % qs).encode()
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
doc = json.loads(payload.decode())
print(doc.get("text", ""), end="")
raise SystemExit(0 if status == 200 else 1)
```

## Gotchas

- On macOS `/tmp` is a symlink to `/private/tmp`. The files root is stored as the physical scratch path. A missing file requested with the `/tmp` spelling is rejected as outside that folder. Take the path from `cd "$RELAY_HOME/scratch" && pwd -P`.
- `ctl api` over TCP is not signed in and returns 401 for this route. Use the control socket.
- The default files root is the account home. Do not list or write `~`. The prelude points this run at the scratch directory.
- Judge the save by the returned size (`wrote-bytes=15` is `marker-files-1` plus a newline). If the saved artifact also contains the request text, a search for the marker passes even when the file is empty.
- The first-run serve log prints a one-time setup code. Do not copy that log into a bundle, a commit or a reply.

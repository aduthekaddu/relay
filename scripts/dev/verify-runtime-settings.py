#!/usr/bin/env python3
"""Isolated REL-135 serve/ptyd integration. Pass binaries built via dev/safe.

All state, credentials and sessions are synthetic. Only allocated fixture
PIDs are stopped. No service manager or installed daemon is used.
"""
import argparse
import concurrent.futures
import http.client
import json
import os
import pathlib
import shutil
import signal
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("fixture", timeout=5)
        self.path = str(path)
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(5)
        self.sock.connect(self.path)

def unix_health(path):
    client = UnixHTTP(path)
    try:
        client.request("GET", "/v1/health")
        return client.getresponse().status == 200
    except OSError:
        return False
    finally:
        client.close()

def reserve_port():
    for port in range(47700, 47800):
        with socket.socket() as listener:
            try:
                listener.bind(("127.0.0.1", port))
                return port
            except OSError:
                pass
    raise RuntimeError("No free Relay development loopback port")

def start_time(pid):
    try:
        fields = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        # An exited zombie cannot keep running or receive a useful kill.
        return None if fields[0] == "Z" else fields[19]
    except FileNotFoundError:
        return None

def stop_process(proc):
    if proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(proc.pid, signal.SIGKILL)
            proc.wait(timeout=5)


def cleanup_fixture(root, procs, streams, terminals, logs, commands, results):
    """Finish every cleanup/reporting step and return failures to the caller."""
    failures = []
    for proc in reversed(procs):
        try:
            stop_process(proc)
        except Exception as error:
            failures.append(f"Stop owned process {proc.pid}: {error}")
    for stream in streams:
        try:
            stream.close()
        except Exception as error:
            failures.append(f"Close owned log: {error}")

    survivors, forced_kills = [], []
    for pid, started in terminals.items():
        try:
            if started is not None and start_time(pid) == started:
                survivors.append(pid)
        except OSError as error:
            failures.append(f"Inspect owned terminal {pid}: {error}")
    for pid in survivors:
        try:
            # Check identity again. Never signal a reused PID or another group.
            if start_time(pid) == terminals[pid]:
                if os.getpgid(pid) == pid:
                    os.killpg(pid, signal.SIGKILL)
                else:
                    os.kill(pid, signal.SIGKILL)
                forced_kills.append(pid)
        except ProcessLookupError:
            pass
        except OSError as error:
            failures.append(f"Kill owned terminal {pid}: {error}")

    remaining = list(survivors)
    deadline = time.monotonic() + 2
    while remaining:
        try:
            remaining = [pid for pid in remaining if start_time(pid) == terminals[pid]]
        except OSError as error:
            failures.append(f"Confirm terminal cleanup: {error}")
            break
        if not remaining or time.monotonic() >= deadline:
            break
        time.sleep(.01)

    race, unreadable = False, False
    for log in logs:
        try:
            detected = "WARNING: DATA RACE" in log.read_text(errors="replace")
            race = race or detected
        except OSError as error:
            unreadable = True
            failures.append(f"Read owned log {log.name}: {error}")
    results["race_warnings"] = True if race else None if unreadable else False
    removed = False
    try:
        shutil.rmtree(root)
        removed = True
    except FileNotFoundError:
        removed = True
    except OSError as error:
        failures.append(f"Remove owned fixture: {error}")
    if race:
        failures.append("Race-instrumented process integration reported a race")
    if survivors:
        failures.append("Owned terminal cleanup required forced kill")
    if remaining:
        failures.append("Owned terminals remain alive after cleanup")
    results["owned_cleanup"] = {
        "processes": [{"pid": p.pid, "exit_code": p.returncode} for p in procs],
        "terminal_pids": list(terminals),
        "survivors": survivors,
        "forced_kills": forced_kills,
        "remaining_survivors": remaining,
        "fixture_removed": removed,
        "errors": failures,
    }
    results["commands"] = commands
    return failures


def scenario(binary, daemon_binary, mode, expect_stale):
    root = pathlib.Path(tempfile.mkdtemp(prefix="rs135-"))
    procs, streams, terminals = [], [], {}
    logs, commands = [], []
    results = {"mode": mode, "expect_stale": expect_stale}
    primary = None
    try:
        port = reserve_port()
        env = dict(os.environ)
        for key in ["RELAY_CONFIG", "RELAY_DOMAIN", "RELAY_PUBLIC_URL", "RELAY_LISTEN", "RELAY_TLS"]:
            env.pop(key, None)
        home, state = root/"home", root/"state"
        env.update(HOME=str(home), RELAY_HOME=str(state), RELAY_NO_PTYD="1", RELAY_LOGIN_ENV="0",
                   RELAY_LISTEN=f"127.0.0.1:{port}", RELAY_TLS="off",
                   RELAY_PUBLIC_URL=f"http://127.0.0.1:{port}",
                   GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null")
        for path in [home/"files", root/"cwd-a", root/"cwd-b", root/"old/repo", root/"next/repo", state/"config"]:
            path.mkdir(parents=True, mode=0o700)
        for label in ["a", "b"]:
            path=root/("shell-"+label)
            path.write_text('#!/bin/sh\nexec /bin/sh "$@"\n')
            path.chmod(0o700)
        for label in ["old", "next"]:
            result=subprocess.run(["git", "init", "-q", "--initial-branch=main", str(root/label/"repo")],env=env,capture_output=True)
            assert result.returncode==0, "Synthetic git init failed"
            commands.append({"argv":["git","init","-q","--initial-branch=main",f"$FIXTURE/{label}/repo"],"exit_code":result.returncode})
        config_file=state/"config/relay.toml"
        config_file.write_text(f'''# Synthetic REL-135 configuration
    [server]
    listen = "127.0.0.1:{port}"
    tls = "off"
    [auth]
    user = "fixture"
    insecure_cookies = true
    [files]
    root = "{home}/files"
    [terminal]
    shell = "{root}/shell-a"
    default_cwd = "{root}/cwd-a"
    record = "off"
    import_tmux = false
    [agents]
    workspace_roots = ["{root}/old"]
    index_history = false
    [usage]
    claude_quota = false
    [desktop]
    enabled = false
    idle_stop = "2h"
    [code]
    enabled = false
    idle_stop = "3h"
    [[apps]]
    id = "synthetic"
    command = ["/bin/false"]
    socket = "synthetic.sock"
    ''')
        config_file.chmod(0o600)
        url=f"http://127.0.0.1:{port}"
        token=None
        results.update(port=port, authenticated="fixture Bearer token; secret never captured")
        def cli(argv, input_text=None):
            result=subprocess.run([binary,*argv],env=env,input=input_text,text=True,capture_output=True,timeout=15)
            commands.append({"argv":["$BINARY",*argv],"stdin":"synthetic password redacted" if input_text else None,"exit_code":result.returncode})
            assert result.returncode==0, "Fixture CLI failed: "+argv[0]
            return result.stdout
        def spawn(executable, command, process_env):
            log=root/(command+"-"+str(len(procs))+".log")
            stream=log.open("wb")
            streams.append(stream);logs.append(log)
            proc=subprocess.Popen([executable,command],env=process_env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
            procs.append(proc)
            commands.append({"argv":["$DAEMON_BINARY" if executable!=binary else "$BINARY",command],"pid":proc.pid,"environment":{"HOME":"$FIXTURE/home","RELAY_HOME":"$FIXTURE/state","RELAY_NO_PTYD":"1","RELAY_LISTEN":f"127.0.0.1:{port}","RELAY_CONFIG":"$FIXTURE/independent.toml" if command=="ptyd" and mode=="different-config" else "default fixture file"}})
            return proc
        def request(method,path,data=None,auth=True):
            body=json.dumps(data).encode() if data is not None else None
            headers={"Content-Type":"application/json"}
            if auth and token: headers["Authorization"]="Bearer "+token
            req=urllib.request.Request(url+path,data=body,headers=headers,method=method)
            try:
                with urllib.request.urlopen(req,timeout=5) as response:
                    status,body=response.status,response.read()
            except urllib.error.HTTPError as error:
                status,body=error.code,error.read()
            return status,json.loads(body) if body else None
        def api(method,path,data=None,expected=200):
            status,body=request(method,path,data)
            assert status==expected, f"{method} {path} expected {expected}, got {status}"
            return body
        def wait_ready(proc,check):
            deadline=time.monotonic()+10
            while time.monotonic()<deadline:
                assert proc.poll() is None,"Owned process exited before readiness"
                try:
                    if check():return
                except (OSError, urllib.error.URLError):
                    pass
                time.sleep(.05)
            raise AssertionError("Owned process readiness timeout")
        def create(data):
            session=api("POST","/api/v1/terminals",data,201)
            terminals[session["pid"]]=start_time(session["pid"])
            return session
        cli(["passwd","--user","fixture","--stdin"],"Temporary-only-passphrase-2026!\n")
        token=json.loads(cli(["token","create","--json","rel135-fixture"]))["token"]
        daemon_env=dict(env)
        if mode=="different-config":
            independent=root/"independent.toml"
            independent.write_text(config_file.read_text());independent.chmod(0o600)
            daemon_env["RELAY_CONFIG"]=str(independent)
        daemon=spawn(daemon_binary,"ptyd",daemon_env)
        wait_ready(daemon,lambda:unix_health(state/"run/ptyd.sock"))
        serve=spawn(binary,"serve",env)
        wait_ready(serve,lambda:request("GET","/api/v1/health",auth=False)[0]==200)
        assert request("GET","/api/v1/settings",auth=False)[0]==401
        old=create({})
        old_repo=str(root/"old/repo");new_repo=str(root/"next/repo")
        assert old["command"][0]==str(root/"shell-a") and old["cwd"]==str(root/"cwd-a") and not old["recording"]
        assert old_repo in [x["path"] for x in api("GET","/api/v1/workspaces")]
        api("POST","/api/v1/workspaces/pin",{"path":old_repo,"pinned":True})
        api("GET","/api/v1/workspaces/git/status?"+urllib.parse.urlencode({"path":old_repo}))
        api("GET","/api/v1/workspaces/git/status?"+urllib.parse.urlencode({"path":new_repo}),expected=403)
        saved=api("PATCH","/api/v1/settings",{"workspaceRoots":[str(root/"next")],"defaultShell":str(root/"shell-b"),"defaultCwd":str(root/"cwd-b"),"recordAgents":True,"claudeQuota":True,"idleMinutes":37})
        new=create({"kind":"agent"})
        listing=[x["path"] for x in api("GET","/api/v1/workspaces")]
        old_status=request("GET","/api/v1/workspaces/git/status?"+urllib.parse.urlencode({"path":old_repo}))[0]
        new_status=request("GET","/api/v1/workspaces/git/status?"+urllib.parse.urlencode({"path":new_repo}))[0]
        stale_terminal=expect_stale or mode in ["legacy-daemon","different-config"]
        if stale_terminal:
            assert new["command"][0]==str(root/"shell-a") and new["cwd"]==str(root/"cwd-a") and not new["recording"]
        else:
            assert new["command"][0]==str(root/"shell-b") and new["cwd"]==str(root/"cwd-b") and new["recording"]
        if expect_stale:
            assert old_repo in listing and new_repo not in listing and old_status==200 and new_status==403
        else:
            assert old_repo not in listing and new_repo in listing and old_status==403 and new_status==200
            want={"normal":"next-session","legacy-daemon":"restart-required","different-config":"different-config"}[mode]
            assert saved["effective"]["terminalStatus"]==want
            assert saved["apply"]["defaultShell"]==want
            assert saved["effective"]["claudeQuota"] and saved["effective"]["codeIdleStop"]=="37m0s"
            assert saved["effective"]["desktopIdleStop"]=="37m0s"
            assert api("GET","/api/v1/agents/quotas")==[], "Invented quota without fixture credentials"
        still=api("GET","/api/v1/terminals/"+old["id"])
        assert still["pid"]==old["pid"] and still["activity"]!="exited" and still["cwd"]==old["cwd"] and not still["recording"]
        client=UnixHTTP(state/"run/ptyd.sock")
        client.request("POST","/v1/sessions/"+old["id"]+"/input",body=b"printf 'REL135_ALIVE\\n'\n")
        assert client.getresponse().status==204;client.close()
        deadline=time.monotonic()+3
        while time.monotonic()<deadline:
            client=UnixHTTP(state/"run/ptyd.sock")
            client.request("GET","/v1/sessions/"+old["id"]+"/snapshot")
            snapshot=json.loads(client.getresponse().read());client.close()
            if "REL135_ALIVE" in snapshot["text"]:break
            time.sleep(.05)
        else:raise AssertionError("Existing session did not accept input")
        assert serve.poll() is None and daemon.poll() is None
        results.update(saved_vs_new_terminal="stale as reproduced" if stale_terminal else "next-session adoption verified",workspace_discovery_and_authorization={"old_status":old_status,"new_status":new_status},existing_session="same PID/cwd/recording and accepts input",save_restarted_processes=False)
        if not expect_stale:
            override=create({"kind":"agent","cwd":str(root/"cwd-a"),"command":["/bin/sh"],"record":False})
            assert override["cwd"]==str(root/"cwd-a") and not override["recording"]
            before=config_file.read_bytes()
            api("PATCH","/api/v1/settings",{"workspaceRoots":[str(root/"missing")]},400)
            assert config_file.read_bytes()==before
            config_file.parent.chmod(0o500)
            try:api("PATCH","/api/v1/settings",{"claudeQuota":False},500)
            finally:config_file.parent.chmod(0o700)
            assert config_file.read_bytes()==before and api("GET","/api/v1/settings")["effective"]["claudeQuota"]
            with config_file.open("a") as out:out.write("\n[future]\nunknown = true\n")
            unknown=config_file.read_bytes()
            api("PATCH","/api/v1/settings",{"claudeQuota":False},409)
            assert config_file.read_bytes()==unknown
            config_file.write_bytes(before)
            def reads():
                for _ in range(60):
                    api("GET","/api/v1/info");api("GET","/api/v1/settings")
            def writes(body):
                for _ in range(20):api("PATCH","/api/v1/settings",body)
            with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
                pending=[pool.submit(reads) for _ in range(3)]+[pool.submit(writes,{"idleMinutes":41}),pool.submit(writes,{"claudeQuota":False})]
                for future in pending:future.result()
            final=api("GET","/api/v1/settings")
            assert final["idleMinutes"]==41 and not final["claudeQuota"] and final["effective"]["codeIdleStop"]=="41m0s"
            stop_process(serve)
            serve=spawn(binary,"serve",env)
            wait_ready(serve,lambda:request("GET","/api/v1/health",auth=False)[0]==200)
            still=api("GET","/api/v1/terminals/"+old["id"])
            assert still["pid"]==old["pid"] and still["activity"]!="exited"
            assert daemon.poll() is None
            results.update(failure_rollback="validation, persistence and unknown fields verified",concurrent_http="360 authenticated GETs and 40 partial PATCHes",serve_restart="existing daemon and session survive")
    except BaseException as error:
        primary = error
        raise
    finally:
        failures = cleanup_fixture(root, procs, streams, terminals, logs, commands, results)
        try:
            print(json.dumps(results, sort_keys=True), flush=True)
        except Exception as error:
            failures.append(f"Report cleanup results: {error}")
        if failures:
            message = "; ".join(failures)
            if primary is None:
                raise AssertionError(message)
            if hasattr(primary, "add_note"):
                primary.add_note(message)


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("--binary",required=True)
    parser.add_argument("--legacy-binary")
    parser.add_argument("--expect-stale",action="store_true")
    args=parser.parse_args()
    binary=str(pathlib.Path(args.binary).resolve())
    scenario(binary,binary,"normal",args.expect_stale)
    if args.legacy_binary and not args.expect_stale:
        scenario(binary,str(pathlib.Path(args.legacy_binary).resolve()),"legacy-daemon",False)
        scenario(binary,binary,"different-config",False)

if __name__=="__main__":
    main()

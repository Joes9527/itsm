#!/usr/bin/env python3
"""Manage only the recorded native processes in the maintained WSL stack."""

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


STATE = Path("/home/administrator/.local/state/itsm-kaf-baseline-20260908")
ORDER = ("itsm", "kaf", "itsm-web", "kaf-web", "itsm-worker-1", "itsm-worker-2")


def identity(pid):
    """Return a process identity that changes when a PID is reused."""
    proc = Path(f"/proc/{pid}")
    try:
        data = (proc / "stat").read_text().rsplit(") ", 1)[1].split()
        if data[0] == "Z":
            return None
        return {"start_ticks": data[19], "cwd": os.readlink(proc / "cwd")}
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        if Path("/proc").exists():
            return None

    # Portable fallback used by local tests and maintenance from macOS.
    result = subprocess.run(
        ["ps", "-p", str(pid), "-o", "state=", "-o", "lstart="],
        text=True,
        capture_output=True,
    )
    value = result.stdout.strip()
    if result.returncode or not value:
        return None
    state, _, started = value.partition(" ")
    if state.startswith("Z") or not started.strip():
        return None
    cwd_result = subprocess.run(
        ["lsof", "-a", "-p", str(pid), "-d", "cwd", "-Fn"],
        text=True,
        capture_output=True,
    )
    cwd = next((line[1:] for line in cwd_result.stdout.splitlines() if line.startswith("n")), None)
    if not cwd:
        return None
    return {"start_ticks": started.strip(), "cwd": cwd}


def load_json(path):
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as error:
        raise RuntimeError(f"cannot read {path}: {error}") from error
    if not isinstance(value, dict):
        raise RuntimeError(f"{path}: expected a JSON object")
    return value


def recipe_path(state, name):
    return state / "config" / f"{name}-launch.json"


def record_path(state, name):
    return state / "evidence" / f"{name}-process.json"


@contextmanager
def lifecycle_lock(state, name, timeout):
    lock_dir = state / "locks"
    lock_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(lock_dir, 0o700)
    path = lock_dir / f"{name}.lock"
    with path.open("a+") as lock_file:
        os.chmod(path, 0o600)
        deadline = time.monotonic() + max(timeout, 0)
        while True:
            try:
                fcntl.flock(lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
                break
            except BlockingIOError:
                if time.monotonic() >= deadline:
                    raise RuntimeError(
                        f"{name}: lifecycle lock is busy; retry after the active operation finishes"
                    )
                time.sleep(0.05)
        try:
            yield
        finally:
            fcntl.flock(lock_file, fcntl.LOCK_UN)


def config_fingerprint(recipe):
    # Include private values in the one-way fingerprint, but never store or print them.
    encoded = json.dumps(recipe, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def process_state(state, name):
    path = record_path(state, name)
    if not path.exists():
        return "absent", None, None
    record = load_json(path)
    try:
        pid = int(record["pid"])
        expected = {"start_ticks": str(record["start_ticks"]), "cwd": record["cwd"]}
    except (KeyError, TypeError, ValueError) as error:
        raise RuntimeError(f"{name}: invalid process record: {error}") from error
    found = identity(pid)
    if found is None:
        return "stale", record, None
    if found != expected:
        return "mismatch", record, found
    return "running", record, found


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def artifact_path(recipe):
    explicit = recipe.get("artifact_path")
    if explicit:
        return Path(explicit)
    cwd = Path(recipe["cwd"])
    argv = recipe.get("argv") or []
    if len(argv) > 1 and not str(argv[1]).startswith("-"):
        candidate = Path(argv[1])
        candidate = candidate if candidate.is_absolute() else cwd / candidate
        if candidate.is_file():
            return candidate
    return cwd / "server.js"


def configured_drift(recipe):
    """Return sanitized configured-versus-actual version drift messages."""
    issues = []
    checks = []
    if recipe.get("executable_sha256"):
        checks.append(("executable_sha256", Path(recipe["argv"][0]), recipe["executable_sha256"]))
    if recipe.get("artifact_sha256"):
        checks.append(("artifact_sha256", artifact_path(recipe), recipe["artifact_sha256"]))
    for label, path, expected in checks:
        try:
            actual = sha256_file(path)
        except OSError:
            actual = "missing"
        if actual != expected:
            issues.append(f"{label} drift (configured {expected}, actual {actual})")

    source_root = recipe.get("source_root")
    expected_revision = recipe.get("source_revision")
    if source_root and expected_revision:
        result = subprocess.run(
            ["git", "-C", source_root, "rev-parse", "HEAD"],
            text=True,
            capture_output=True,
        )
        actual = result.stdout.strip() if result.returncode == 0 else "unavailable"
        short_match = len(expected_revision) >= 7 and actual.startswith(expected_revision)
        if actual != expected_revision and not short_match:
            issues.append(f"source_revision drift (configured {expected_revision}, actual {actual})")

    expected_build = recipe.get("build_id")
    if expected_build:
        roots = [Path(recipe["cwd"]), Path(source_root)] if source_root else [Path(recipe["cwd"])]
        candidates = []
        for root in roots:
            candidates.extend((root / "BUILD_ID", root / ".next" / "BUILD_ID", root / "itsm-frontend" / ".next" / "BUILD_ID"))
        actual = "missing"
        for candidate in candidates:
            if candidate.is_file():
                actual = candidate.read_text().strip()
                break
        if actual != expected_build:
            issues.append(f"build_id drift (configured {expected_build}, actual {actual})")
    return issues


def listening_inodes(port):
    target = f"{int(port):04X}"
    inodes = set()
    for table in (Path("/proc/net/tcp"), Path("/proc/net/tcp6")):
        try:
            lines = table.read_text().splitlines()[1:]
        except OSError:
            continue
        for line in lines:
            fields = line.split()
            if fields[1].rsplit(":", 1)[-1].upper() == target and fields[3] == "0A":
                inodes.add(fields[9])
    return inodes


def port_owners(port):
    inodes = listening_inodes(port)
    owners = set()
    if Path("/proc/net/tcp").exists():
        if not inodes:
            return []
        for fd in Path("/proc").glob("[0-9]*/fd/*"):
            try:
                target = os.readlink(fd)
            except (OSError, PermissionError):
                continue
            if target.startswith("socket:[") and target[8:-1] in inodes:
                owners.add(fd.parts[2])
        # The listener is known even when procfs permissions hide its PID.
        return sorted(owners) if owners else ["unknown"]

    # lsof is the portable fallback. Failure or no output means no known owner.
    result = subprocess.run(
        ["lsof", "-nP", f"-iTCP:{int(port)}", "-sTCP:LISTEN", "-t"],
        text=True,
        capture_output=True,
    )
    for value in result.stdout.split():
        if value.isdigit():
            owners.add(value)
    return sorted(owners)


def process_parent(pid):
    try:
        data = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
        return int(data[1])
    except (FileNotFoundError, ProcessLookupError, PermissionError):
        if Path("/proc").exists():
            return None
    result = subprocess.run(
        ["ps", "-p", str(pid), "-o", "ppid="], text=True, capture_output=True
    )
    value = result.stdout.strip()
    return int(value) if result.returncode == 0 and value.isdigit() else None


def belongs_to_process_tree(pid, root_pid):
    current = pid
    seen = set()
    for _ in range(128):
        if current == root_pid:
            return True
        if current <= 1 or current in seen:
            return False
        seen.add(current)
        parent = process_parent(current)
        if parent is None:
            return False
        current = parent
    return False


def port_drift(recipe, root_pid):
    port = recipe.get("port")
    if not port:
        return []
    owners = port_owners(port)
    if not owners:
        return [f"port {port} has no listener"]
    if "unknown" in owners:
        return [f"port {port} listener owner is unknown"]
    foreign = [
        owner for owner in owners
        if not belongs_to_process_tree(int(owner), root_pid)
    ]
    if foreign:
        return [f"port {port} is owned by foreign PID(s) {','.join(foreign)}"]
    return []


def describe_metadata(recipe):
    parts = []
    for key in ("source_revision", "build_id"):
        if recipe.get(key):
            parts.append(f"{key}={recipe[key]}")
    return ("; " + ", ".join(parts)) if parts else ""


def status(state, name):
    recipe = load_json(recipe_path(state, name)) if recipe_path(state, name).exists() else None
    state_name, record, actual = process_state(state, name)
    configured_problems = configured_drift(recipe) if recipe else []
    if state_name == "mismatch":
        port_note = ""
        if recipe and recipe.get("port"):
            owners = port_owners(recipe["port"])
            if owners:
                port_note = f"; port {recipe['port']} owned by actual PID(s) {','.join(map(str, owners))}"
        print(f"{name}: identity mismatch for recorded PID {record['pid']}; actual PID {record['pid']} has start_ticks={actual['start_ticks']} cwd={actual['cwd']}{port_note}")
        return 1
    if state_name == "stale":
        details = []
        if recipe and recipe.get("port"):
            owners = port_owners(recipe["port"])
            if owners:
                details.append(f"configured port {recipe['port']} is owned by actual PID(s) {','.join(map(str, owners))}")
        details.extend(configured_problems)
        if details:
            print(f"{name}: recorded PID {record['pid']} is gone; " + "; ".join(details))
            return 1
        print(f"{name}: stopped (stale record for PID {record['pid']})")
        return 0
    if state_name == "absent":
        details = []
        if recipe and recipe.get("port"):
            owners = port_owners(recipe["port"])
            if owners:
                details.append(f"configured port {recipe['port']} is owned by actual PID(s) {','.join(map(str, owners))}")
        details.extend(configured_problems)
        if details:
            print(f"{name}: stopped with " + "; ".join(details))
            return 1
        print(f"{name}: stopped")
        return 0

    problems = []
    if recipe:
        recorded_fingerprint = record.get("config_sha256")
        if recorded_fingerprint and recorded_fingerprint != config_fingerprint(recipe):
            problems.append("runtime configuration drift")
        problems.extend(configured_problems)
        problems.extend(port_drift(recipe, int(record["pid"])))
    suffix = describe_metadata(recipe or {})
    if problems:
        print(f"{name}: running (PID {record['pid']}) with " + "; ".join(problems) + suffix)
        return 1
    print(f"{name}: running (PID {record['pid']}){suffix}")
    return 0


def start(state, name, startup_timeout=60.0):
    path = recipe_path(state, name)
    if not path.exists():
        print(f"{name}: not configured")
        return 0
    recipe = load_json(path)
    state_name, record, actual = process_state(state, name)
    if state_name == "mismatch":
        raise RuntimeError(f"{name}: identity mismatch for recorded PID {record['pid']}; refusing to replace its record")
    if state_name == "running":
        problems = []
        if record.get("config_sha256") and record["config_sha256"] != config_fingerprint(recipe):
            problems.append("runtime configuration drift")
        problems.extend(configured_drift(recipe))
        problems.extend(port_drift(recipe, int(record["pid"])))
        if problems:
            raise RuntimeError(f"{name}: running (PID {record['pid']}) with " + "; ".join(problems))
        print(f"{name}: running (PID {record['pid']}){describe_metadata(recipe)}")
        return 0
    if not recipe.get("ready", False):
        raise RuntimeError(f"{name}: configuration has not passed the startup review")
    if configured_drift(recipe):
        raise RuntimeError(f"{name}: configured artifact/source does not match: " + "; ".join(configured_drift(recipe)))
    port = recipe.get("port")
    if port:
        owners = port_owners(port)
        if owners:
            raise RuntimeError(f"{name}: port {port} is occupied by PID(s) {','.join(map(str, owners))}; refusing to spawn")

    env = {key: os.environ[key] for key in ("HOME", "LANG", "TZ") if key in os.environ}
    env["PATH"] = "/home/administrator/.local/bin:/usr/local/bin:/usr/bin:/bin"
    env.update(recipe.get("env", {}))
    logpath = state / "logs" / f"{name}.log"
    logpath.parent.mkdir(parents=True, exist_ok=True)
    with logpath.open("ab") as output:
        process = subprocess.Popen(
            recipe["argv"], cwd=recipe["cwd"], env=env,
            stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT,
            start_new_session=True,
        )
    time.sleep(0.25)
    found = identity(process.pid)
    if not found:
        raise RuntimeError(f"{name}: exited during startup; see {logpath}")
    record = dict(
        found,
        pid=process.pid,
        argv=recipe["argv"],
        log_path=str(logpath),
        config_sha256=config_fingerprint(recipe),
    )
    record_path(state, name).parent.mkdir(parents=True, exist_ok=True)
    record_path(state, name).write_text(json.dumps(record, indent=2) + "\n")
    if port:
        deadline = time.monotonic() + startup_timeout
        last_problems = port_drift(recipe, process.pid)
        while last_problems and time.monotonic() < deadline:
            if identity(process.pid) != found:
                raise RuntimeError(
                    f"{name}: exited before its configured port {port} became ready; "
                    f"see {logpath}"
                )
            time.sleep(0.1)
            last_problems = port_drift(recipe, process.pid)
        if last_problems:
            raise RuntimeError(
                f"{name}: listener did not become ready before timeout; "
                + "; ".join(last_problems)
                + f"; recorded PID {process.pid} remains available for exact cleanup"
            )
        if identity(process.pid) != found:
            raise RuntimeError(
                f"{name}: process identity changed after port {port} became ready; "
                "record remains available for exact cleanup"
            )
    print(f"{name}: started (PID {process.pid}); see {logpath}{describe_metadata(recipe)}")
    return 0


def stop(state, name):
    state_name, record, actual = process_state(state, name)
    if state_name == "absent":
        print(f"{name}: stopped")
        return 0
    if state_name == "stale":
        print(f"{name}: stopped (stale record for PID {record['pid']})")
        return 0
    if state_name == "mismatch":
        raise RuntimeError(f"{name}: identity mismatch for recorded PID {record['pid']}; refusing to stop actual process")

    pid = int(record["pid"])
    # Recheck immediately before signalling. A stale record must never target a
    # reused PID, and an adopted non-leader must never terminate its shared group.
    current_identity = identity(pid)
    expected_identity = {"start_ticks": str(record["start_ticks"]), "cwd": record["cwd"]}
    if current_identity != expected_identity:
        raise RuntimeError(f"{name}: identity changed before signal; refusing to stop actual process")
    os.kill(pid, signal.SIGTERM)
    for _ in range(100):
        current, _, _ = process_state(state, name)
        if current != "running":
            print(f"{name}: stopped")
            return 0
        time.sleep(0.1)
    raise RuntimeError(f"{name}: still stopping; no forced termination was sent")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--state", type=Path, default=STATE)
    parser.add_argument("--startup-timeout", type=float, default=60.0)
    parser.add_argument("--lock-timeout", type=float, default=2.0)
    parser.add_argument("action", choices=("start", "stop", "status"))
    parser.add_argument("service", nargs="?", choices=ORDER)
    args = parser.parse_args()
    names = (args.service,) if args.service else (tuple(reversed(ORDER)) if args.action == "stop" else ORDER)
    failed = False
    for name in names:
        try:
            with lifecycle_lock(args.state, name, args.lock_timeout):
                if args.action == "start":
                    result = start(args.state, name, args.startup_timeout)
                elif args.action == "stop":
                    result = stop(args.state, name)
                else:
                    result = status(args.state, name)
            failed = failed or bool(result)
        except RuntimeError as error:
            print(error, file=sys.stderr)
            failed = True
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())

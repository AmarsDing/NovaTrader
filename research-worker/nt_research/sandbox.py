"""RD-Agent runs only in a pinned, offline Linux container, never on the host."""
import os
import platform
import re
import shutil
import stat
import subprocess
import threading
import time
from .protocol import Invalid, Unavailable, local_file, sha

MAX_OUTPUT_BYTES = 64 * 1024 * 1024


def bundle_files(data, root):
    if data.get("kind") != "rd_agent_bundle" or data.get("adapter") != "novatrader-qlib-v1":
        raise Invalid("frozen Qlib adapter bundle required")
    files = data.get("files", [])
    if not files or len(files) > 1000:
        raise Invalid("missing or excessive bundle files")
    seen = set(); total = 0
    for descriptor in files:
        name = descriptor.get("path")
        if not isinstance(name, str) or not re.fullmatch(r"[A-Za-z0-9_./-]+", name) or ".." in name.split("/") or name in seen:
            raise Invalid("bundle paths must be unique portable relative names")
        seen.add(name)
        path = local_file(root, name)
        total += path.stat().st_size
        if total > MAX_OUTPUT_BYTES or sha(path.read_bytes()) != descriptor.get("sha256"):
            raise Invalid("RD input hash/size mismatch")
    if data.get("base_features_path") not in seen:
        raise Invalid("base features must be frozen in the bundle")
    return files


def command(job, data, root, output):
    opts = job.get("options", {})
    image = opts.get("image", "")
    if not re.fullmatch(r"[A-Za-z0-9./:_-]+@sha256:[0-9a-f]{64}", image):
        raise Invalid("RD-Agent requires an approved image digest, never latest")
    bundle_files(data, root)
    loops = opts.get("max_loops", 1); seconds = opts.get("timeout_seconds", 60)
    if type(loops) is not int or not 1 <= loops <= 5 or type(seconds) is not int or not 1 <= seconds <= 300:
        raise Invalid("invalid bounded research budget")
    if "," in str(root) or "," in str(output):
        raise Invalid("Docker bind paths cannot contain commas")
    return ["docker", "run", "--pull=never", "--name", "nt-research-" + job["run_id"],
            "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
            "--pids-limit=128", "--cpus=2", "--memory=2g", "--user=65534:65534",
            "--tmpfs=/tmp:rw,noexec,nosuid,size=256m", "--mount", f"type=bind,src={root.resolve()},dst=/input,readonly",
            "--mount", f"type=bind,src={output.resolve()},dst=/output", "--workdir=/output", image,
            "rdagent", "fin_factor", "--loop_n", str(loops), "--all_duration", f"{seconds}s",
            "--base_features_path", "/input/" + data["base_features_path"]]


def stage_inputs(data, root, target):
    files = bundle_files(data, root)
    target.mkdir()
    for descriptor in files:
        content = local_file(root, descriptor["path"]).read_bytes()
        if sha(content) != descriptor["sha256"]:
            raise Invalid("RD input changed while staging")
        path = target / descriptor["path"]
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content)


def artifact_budget(output):
    total = count = 0
    for current, dirs, files in os.walk(output, followlinks=False):
        for name in dirs + files:
            path = os.path.join(current, name)
            info = os.lstat(path)
            if stat.S_ISLNK(info.st_mode) or (not stat.S_ISREG(info.st_mode) and not stat.S_ISDIR(info.st_mode)):
                return False
            count += 1; total += info.st_size if stat.S_ISREG(info.st_mode) else 0
            if count > 1000 or total > MAX_OUTPUT_BYTES:
                return False
    return True


def rd_agent(job, data, output, root):
    # Only explicitly listed files are mounted. Sibling job files and the host
    # result.json are never exposed to the container.
    command(job, data, root, output)
    if platform.system() != "Linux":
        raise Unavailable("RD-Agent requires Linux container host")
    if shutil.which("docker") is None:
        raise Unavailable("Docker is unavailable")
    inputs = output / "rd-input"
    stage_inputs(data, root, inputs)
    quarantine = output / "rd-quarantine"
    quarantine.mkdir(mode=0o777); quarantine.chmod(0o777)
    argv = command(job, data, inputs, quarantine)
    env = {key: os.environ[key] for key in ("PATH", "SYSTEMROOT", "WINDIR") if key in os.environ}
    start = time.monotonic(); state = "failed"
    # Drain into fixed-size tails instead of unbounded capture_output buffers.
    tails = [bytearray(), bytearray()]
    def drain(pipe, tail):
        while chunk := pipe.read(8192):
            tail.extend(chunk)
            del tail[:-65536]
        pipe.close()
    proc = subprocess.Popen(argv, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    readers = [threading.Thread(target=drain, args=(pipe, tail), daemon=True)
               for pipe, tail in zip((proc.stdout, proc.stderr), tails)]
    for reader in readers:
        reader.start()
    try:
        while proc.poll() is None:
            if time.monotonic() - start >= job.get("options", {}).get("timeout_seconds", 60):
                state = "timed_out"; break
            if not artifact_budget(quarantine):
                state = "artifact_budget_exceeded"; break
            time.sleep(.1)
        else:
            state = "succeeded" if proc.returncode == 0 else "failed"
        if not artifact_budget(quarantine):
            state = "artifact_budget_exceeded"
    finally:
        try:
            subprocess.run(["docker", "rm", "-f", "nt-research-" + job["run_id"]], env=env,
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15, check=False)
        except (OSError, subprocess.TimeoutExpired):
            state = "cleanup_failed"
        if proc.poll() is None:
            proc.kill()
        proc.wait(timeout=5)
        for reader in readers:
            reader.join(timeout=1)
    (output / "rd.stdout.txt").write_bytes(bytes(tails[0]))
    (output / "rd.stderr.txt").write_bytes(bytes(tails[1]))
    return {"state": state, "exit_code": proc.returncode, "duration_seconds": time.monotonic() - start,
            "network": "none", "max_external_model_spend": 0,
            "artifact_policy": "quarantined; never import or execute on host",
            "promotion": "requires_review_and_Go_backtest",
            "note": "Pinned image needs an offline cached backend and matching Qlib bundle; no host keys are passed. Output size is polled, not a filesystem quota."}

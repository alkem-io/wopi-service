"""Run the WOPI final-image check with fresh synthetic peers and exact-ID cleanup."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


endpoint = json.loads(docker("context", "inspect"))[0]["Endpoints"]["docker"]["Host"]
if not endpoint.startswith("unix://") or os.environ.get("DOCKER_HOST", endpoint) != endpoint:
    raise SystemExit("Only the verified local Unix Docker endpoint is allowed")
evidence = Path(os.environ["EVIDENCE"]).resolve()
if not evidence.is_dir():
    raise SystemExit("EVIDENCE must already exist")
image = sys.argv[1]
if not image.startswith("sha256:") or docker("image", "inspect", "--format", "{{.Id}}", image) != image:
    raise SystemExit("An immutable local image ID is required")
prefix = "forge091-wopi-" + uuid.uuid4().hex[:12]
stage = evidence / prefix
stage.mkdir()
arch = docker("image", "inspect", "--format", "{{.Architecture}}", image)
subprocess.run(["go", "build", "-o", str(stage / "fixture"), ".scripts/dependency-image-smoke/main.go"], check=True,
               env={**os.environ, "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": arch})
os.chmod(stage / "fixture", 0o755)
images = {"wopi": image, "db": docker("image", "inspect", "--format", "{{.Id}}", os.environ.get("SMOKE_POSTGRES_IMAGE", "postgres:17-alpine")),
          "broker": docker("image", "inspect", "--format", "{{.Id}}", os.environ.get("SMOKE_RABBITMQ_IMAGE", "rabbitmq:3-alpine"))}
planned = {"prefix": prefix, "network": prefix, "volumes": [prefix + "-db", prefix + "-broker"],
           "containers": [prefix + "-" + name for name in ["db", "broker", "fixture", "wopi"]],
           "images": images, "host_ports": [], "environment": "synthetic explicit values only", "binds": [str(stage / "fixture")]}
(evidence / "resources.json").write_text(json.dumps(planned, indent=2))
for kind, command in [("containers-before", ["ps", "-a", "--format", "{{json .}}"]),
                      ("networks-before", ["network", "ls", "--format", "{{json .}}"]),
                      ("volumes-before", ["volume", "ls", "--format", "{{json .}}"])]:
    (evidence / (kind + ".jsonl")).write_text(docker(*command) + "\n")
for resource, names in [("container", planned["containers"]), ("volume", planned["volumes"]), ("network", [prefix])]:
    for name in names:
        if subprocess.run(["docker", resource, "inspect", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            raise SystemExit("Resource collision: " + name)
created = {"containers": [], "volumes": [], "networks": []}
cleanup_errors = []
passed = False


def record():
    (evidence / "created-resources.json").write_text(json.dumps(created, indent=2))


def start(name, args, image_id):
    cid = docker("create", "--pull=never", "--name", prefix + "-" + name, "--network", prefix,
                 "--network-alias", name, "--label", "forge.run=" + prefix, *args, image_id)
    created["containers"].append(cid)
    record()
    docker("start", cid)
    return cid


def ready(command, attempts=90):
    for _ in range(attempts):
        if subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError("Readiness failed: " + repr(command))


try:
    created["networks"].append(docker("network", "create", "--internal", "--label", "forge.run=" + prefix, prefix))
    record()
    for name in planned["volumes"]:
        created["volumes"].append(docker("volume", "create", "--label", "forge.run=" + prefix, name))
        record()
    db = start("db", ["-e", "POSTGRES_USER=smoke", "-e", "POSTGRES_PASSWORD=smoke", "-e", "POSTGRES_DB=wopi", "-v", prefix + "-db:/var/lib/postgresql/data"], images["db"])
    broker = start("broker", ["-e", "RABBITMQ_DEFAULT_USER=smoke", "-e", "RABBITMQ_DEFAULT_PASS=smoke", "-e", "RABBITMQ_SERVER_ADDITIONAL_ERL_ARGS=+S 2:2", "-v", prefix + "-broker:/var/lib/rabbitmq"], images["broker"])
    ready(["docker", "exec", db, "pg_isready", "-U", "smoke", "-d", "wopi"])
    ready(["docker", "exec", broker, "rabbitmq-diagnostics", "-q", "ping"])
    fixture = start("fixture", ["--entrypoint", "/smoke-fixture", "--mount", "type=bind,src=" + str(stage / "fixture") + ",dst=/smoke-fixture,readonly"], image)
    ready(["docker", "exec", fixture, "/smoke-fixture", "broker-ready"])
    args = []
    for key, value in {"WOPI_DATABASE_HOST": "db", "WOPI_DATABASE_USERNAME": "smoke", "WOPI_DATABASE_PASSWORD": "smoke", "WOPI_DATABASE_NAME": "wopi", "AUTH_SERVICE_URL": "http://fixture:8090", "FILE_SERVICE_URL": "http://fixture:8090", "WOPI_COLLABORA_URL": "http://fixture:8090", "WOPI_BASE_URL": "http://wopi:8080", "WOPI_TOKEN_SECRET": "synthetic-private-test-secret", "WOPI_PROOF_VALIDATION": "true", "RABBITMQ_URL": "amqp://smoke:smoke@broker:5672/"}.items():
        args += ["-e", key + "=" + value]
    wopi = start("wopi", args, image)
    ready(["docker", "exec", fixture, "/smoke-fixture", "ready"])
    result = subprocess.run(["docker", "exec", fixture, "/smoke-fixture", "check"], text=True, capture_output=True)
    (evidence / "smoke.log").write_text(result.stdout + result.stderr)
    print(result.stdout + result.stderr, end="")
    if result.returncode:
        raise RuntimeError("Final-image WOPI assertions failed")
    passed = True
finally:
    for cid in created["containers"]:
        try:
            (evidence / (cid + ".log")).write_text(docker("logs", cid))
            docker("rm", "-f", cid)
        except subprocess.CalledProcessError as exc:
            cleanup_errors.append(str(exc))
    for volume in created["volumes"]:
        try:
            docker("volume", "rm", volume)
        except subprocess.CalledProcessError as exc:
            cleanup_errors.append(str(exc))
    for network in created["networks"]:
        try:
            docker("network", "rm", network)
        except subprocess.CalledProcessError as exc:
            cleanup_errors.append(str(exc))
    for resource, ids in [("container", created["containers"]), ("volume", created["volumes"]), ("network", created["networks"])]:
        for resource_id in ids:
            if subprocess.run(["docker", resource, "inspect", resource_id], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
                cleanup_errors.append("Resource remains: " + resource_id)
    (evidence / "smoke-result.json").write_text(json.dumps({"pass": passed, "image": image, "resources": planned, "created": created, "cleanup_errors": cleanup_errors}, indent=2))
    if cleanup_errors:
        raise RuntimeError("Fixture cleanup failed: " + repr(cleanup_errors))

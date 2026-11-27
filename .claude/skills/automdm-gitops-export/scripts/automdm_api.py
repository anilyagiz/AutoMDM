"""Read-only access to AutoMDM through automdmctl.

Every call goes through `automdmctl`, so this code never sees the API token:
automdmctl reads it from its own config (and --config/--context pick which one).
The only subcommands allowed are `api` without -X (always GET), `new`,
`--version`, `config get address`, and `gitops` with --dry-run hardcoded.
Anything else raises before a process starts.
"""

from __future__ import annotations

import json
import re
import subprocess
from concurrent.futures import ThreadPoolExecutor

# GET endpoints this skill reads. Each is one the generate-gitops command
# relies on, or the documented download (alt=media) form of one.
ALLOWED_PATHS = [
    r"/api/latest/automdm/me",
    r"/api/latest/automdm/version",
    r"/api/latest/automdm/config",
    r"/api/latest/automdm/spec/enroll_secret",
    r"/api/latest/automdm/spec/labels",
    r"/api/latest/automdm/spec/certificate_authorities",
    r"/api/latest/automdm/teams",
    r"/api/latest/automdm/teams/\d+",
    r"/api/latest/automdm/policies",
    r"/api/latest/automdm/reports",
    r"/api/latest/automdm/scripts",
    r"/api/latest/automdm/scripts/\d+",
    r"/api/latest/automdm/configuration_profiles",
    r"/api/latest/automdm/configuration_profiles/[A-Za-z0-9_\-]+",
    r"/api/latest/automdm/assets",
    r"/api/latest/automdm/assets/[A-Za-z0-9_\-]+",
    r"/api/latest/automdm/certificates",
    r"/api/latest/automdm/custom_host_vitals",
    r"/api/latest/automdm/microsoft_graph_credentials",
    r"/api/latest/automdm/vpp_tokens",
    r"/api/latest/automdm/logo",
    r"/api/latest/automdm/setup_experience/eula/metadata",
    r"/api/latest/automdm/setup_experience/eula/[A-Za-z0-9_\-]+",
    r"/api/latest/automdm/setup_experience/software",
    r"/api/latest/automdm/setup_experience/script",
    r"/api/latest/automdm/enrollment_profiles/automatic",
    r"/api/latest/automdm/bootstrap/\d+/metadata",
    r"/api/latest/automdm/software/titles",
    r"/api/latest/automdm/software/titles/\d+",
    r"/api/latest/automdm/software/titles/\d+/icon",
    r"/api/latest/automdm/software/automdm_maintained_apps",
    r"/api/latest/automdm/software/automdm_maintained_apps/\d+",
]
_ALLOWED = [re.compile(p + r"$") for p in ALLOWED_PATHS]

_STATUS = re.compile(r"non 2XX return of (\d+)")

# Seconds to wait for automdmctl.
INFO_TIMEOUT = 60  # --version, new --help, config get
NEW_TIMEOUT = 120
API_TIMEOUT = 300  # one GET, which can be a large download
DRY_RUN_TIMEOUT = 1800


class AutoMDMctlError(Exception):
    def __init__(self, message: str, status: int | None = None):
        super().__init__(message)
        self.status = status


class AutoMDMctl:
    def __init__(self, binary: str = "automdmctl", config: str | None = None, context: str | None = None):
        self.binary = binary
        self.config = config
        self.context = context

    def _conn_flags(self) -> list[str]:
        flags = []
        if self.config:
            flags += ["--config", self.config]
        if self.context:
            flags += ["--context", self.context]
        return flags

    def _run(self, args: list[str], env: dict | None = None, cwd: str | None = None, timeout: int = API_TIMEOUT):
        return subprocess.run([self.binary] + args, capture_output=True, env=env, cwd=cwd, timeout=timeout)

    def version(self) -> str:
        r = self._run(["--version"], timeout=INFO_TIMEOUT)
        if r.returncode:
            raise AutoMDMctlError(r.stderr.decode(errors="replace").strip() or "automdmctl --version failed")
        m = re.search(r"version\s+(\S+)", r.stdout.decode(errors="replace"))
        return m.group(1) if m else "unknown"

    def has_new(self) -> bool:
        r = self._run(["new", "--help"], timeout=INFO_TIMEOUT)
        return r.returncode == 0 and b"--org-name" in r.stdout

    def address(self) -> str:
        r = self._run(["config", "get"] + self._conn_flags() + ["address"], timeout=INFO_TIMEOUT)
        out = r.stdout.decode(errors="replace")
        m = re.search(r"=>\s*(\S+)", out)
        if r.returncode or not m:
            raise AutoMDMctlError("couldn't read the address from automdmctl's config; run `automdmctl login` or `automdmctl config set --address`")
        return m.group(1)

    def new(self, directory: str, org_name: str, force: bool = False) -> str:
        args = ["new", "--dir", directory, "--org-name", org_name]
        if force:
            args.append("--force")
        r = self._run(args, timeout=NEW_TIMEOUT)
        if r.returncode:
            raise AutoMDMctlError(r.stderr.decode(errors="replace").strip() or r.stdout.decode(errors="replace").strip())
        return r.stdout.decode(errors="replace")

    def gitops_dry_run(self, files: list[str], cwd: str, env: dict, delete_other_teams: bool = True):
        args = ["gitops"] + self._conn_flags()
        for f in files:
            args += ["-f", f]
        if delete_other_teams:
            args.append("--delete-other-teams")
        args.append("--dry-run")
        return self._run(args, env=env, cwd=cwd, timeout=DRY_RUN_TIMEOUT)

    def get(self, path: str, params: dict | None = None) -> bytes:
        if not any(p.match(path) for p in _ALLOWED):
            raise AutoMDMctlError(f"refusing to call {path}: not an allowlisted read-only endpoint")
        args = ["api"] + self._conn_flags()
        for k, v in (params or {}).items():
            args += ["-F", f"{k}={v}"]
        args.append(path)
        r = self._run(args)
        if r.returncode:
            err = r.stderr.decode(errors="replace").strip()
            m = _STATUS.search(err)
            raise AutoMDMctlError(f"GET {path}: {err}", int(m.group(1)) if m else None)
        return r.stdout


class API:
    """JSON helpers over AutoMDMctl.get, with pagination and parallel fetches."""

    def __init__(self, automdmctl: AutoMDMctl, workers: int = 8):
        self.fc = automdmctl
        self.workers = workers

    def raw(self, path: str, params: dict | None = None) -> bytes:
        return self.fc.get(path, params)

    def json(self, path: str, params: dict | None = None) -> dict:
        body = self.fc.get(path, params)
        return json.loads(body) if body.strip() else {}

    def maybe_json(self, path: str, params: dict | None = None, missing=(404,)):
        try:
            return self.json(path, params)
        except AutoMDMctlError as e:
            if e.status in missing:
                return None
            raise

    def maybe_raw(self, path: str, params: dict | None = None, missing=(404,)):
        try:
            return self.raw(path, params)
        except AutoMDMctlError as e:
            if e.status in missing:
                return None
            raise

    def paged(self, path: str, key: str, params: dict | None = None, per_page: int = 500) -> list:
        items: list = []
        page = 0
        while True:
            p = dict(params or {})
            p.update({"per_page": per_page, "page": page})
            d = self.json(path, p)
            batch = d.get(key) or []
            items.extend(batch)
            if "meta" in d:
                if not (d["meta"] or {}).get("has_next_results"):
                    break
            elif len(batch) < per_page:
                break
            if not batch or page >= 1000:
                break
            page += 1
        return items

    def pmap(self, fn, items):
        items = list(items)
        if len(items) <= 1:
            return [fn(i) for i in items]
        with ThreadPoolExecutor(max_workers=self.workers) as ex:
            return list(ex.map(fn, items))

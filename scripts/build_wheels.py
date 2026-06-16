#!/usr/bin/env python3
"""Build PyPI platform wheels that vendor the prebuilt Go binary (#160).

This is the wheel analogue of scripts/build-mcpb.sh: it wraps each
cross-compiled GoReleaser binary from dist/ into a platform-tagged wheel so the
server installs via `uvx web-researcher-mcp`, `uv tool install web-researcher-mcp`,
or `pip install web-researcher-mcp` with NO Go toolchain.

Deliberately STDLIB-ONLY (zipfile/hashlib/base64/csv) — no third-party build
backend, no go-to-wheel dependency. The mandate is minimal-to-zero dependencies;
a hand-rolled wheel zip keeps the build self-contained and fully auditable, and
the produced wheels themselves carry no Python dependency at all (pure binary +
a tiny exec shim).

Each wheel is `py3-none-<platform>`: the `none` ABI makes one wheel per platform
serve every Python 3.x (there is no compiled extension — just a launcher that
execs the bundled Go binary). pip/uv select the right wheel by platform tag.

Usage:
    python3 scripts/build_wheels.py <version> [--dist dist] [--out dist]

Writes <out>/*.whl. Idempotent: same inputs → byte-stable wheels (mod_time
pinned), so re-runs don't churn.
"""

import argparse
import base64
import csv
import hashlib
import io
import os
import re
import sys
import zipfile

# A pragmatic PEP 440 release-version check (release + optional pre/post/dev/local).
# We validate early and fail loud: a bad version silently produces wheels that
# only PyPI/twine reject much later. Releases use clean SemVer (e.g. 1.25.0), so
# this is strict-enough without reimplementing the full grammar.
_PEP440_RE = re.compile(
    r"^[0-9]+(\.[0-9]+)*"          # release segment: 1 / 1.25 / 1.25.0
    r"((a|b|rc)[0-9]+)?"           # optional pre-release
    r"(\.post[0-9]+)?"             # optional post-release
    r"(\.dev[0-9]+)?"              # optional dev-release
    r"(\+[a-zA-Z0-9]+(\.[a-zA-Z0-9]+)*)?$"  # optional local version
)

# Distribution + import names. PyPI normalizes the dist name; the import package
# must be a valid identifier (underscores).
DIST_NAME = "web-researcher-mcp"
PKG_NAME = "web_researcher_mcp"
BINARY = "web-researcher-mcp"
SUMMARY = (
    "Your AI research assistant that cites real sources and stays honest — "
    "web search, content extraction, and multi-source research over MCP."
)
HOMEPAGE = "https://github.com/zoharbabin/web-researcher-mcp"
AUTHOR = "Zohar Babin"
REQUIRES_PYTHON = ">=3.10"

# A fixed timestamp inside the zip keeps wheels reproducible across rebuilds
# (1980-01-01, the zip epoch floor — value is irrelevant, only its stability is).
ZIP_EPOCH = (1980, 1, 1, 0, 0, 0)

# GoReleaser (goos, goarch) → the wheel platform tag(s) it maps to. A static
# CGO_ENABLED=0 Go binary runs on both glibc and musl, but installers match by
# TAG not by actual libc: pip/uv on Alpine refuse a manylinux wheel, so linux
# ships BOTH manylinux and musllinux wheels carrying the same binary.
PLATFORMS = [
    ("darwin", "amd64", ["macosx_10_9_x86_64"]),
    ("darwin", "arm64", ["macosx_11_0_arm64"]),
    ("linux", "amd64", ["manylinux_2_17_x86_64", "musllinux_1_2_x86_64"]),
    ("linux", "arm64", ["manylinux_2_17_aarch64", "musllinux_1_2_aarch64"]),
    ("windows", "amd64", ["win_amd64"]),
]

SHIM = '''"""web-researcher-mcp — Go binary packaged as a Python wheel.

The wheel vendors the prebuilt, signed Go binary; this shim locates it and
hands off the process so `uvx web-researcher-mcp` behaves exactly like running
the binary directly (an MCP server over STDIO, so stdio/argv/signal/exit-code
fidelity matters).
"""

import os
import stat
import subprocess
import sys

__version__ = "{version}"

_BINARY_NAME = "{binary}{exe}"


def get_binary_path():
    """Absolute path to the bundled binary, made runnable on demand.

    Exported so other Python code can locate the binary without launching it.
    Two install-time facts have to be repaired here:
      1. Wheels don't reliably preserve the Unix exec bit — restore it.
      2. On macOS, a file written by the installer carries a quarantine /
         provenance xattr; Gatekeeper then SIGKILLs (signal 9) this notarized
         binary the first time it's exec'd from the Python process. Clearing the
         com.apple.* xattrs lets the (Developer-ID-signed, notarized) binary run.
         Best-effort: ignore failures (non-macOS, no xattr support, read-only fs).
    """
    path = os.path.join(os.path.dirname(__file__), "bin", _BINARY_NAME)
    if sys.platform == "win32":
        return path
    mode = os.stat(path).st_mode
    if not mode & stat.S_IXUSR:
        os.chmod(path, mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    if sys.platform == "darwin":
        # `xattr -c` strips com.apple.quarantine / com.apple.provenance, which
        # otherwise cause a SIGKILL on first exec of a wheel-delivered binary.
        try:
            subprocess.run(
                ["/usr/bin/xattr", "-c", path],
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
        except OSError:
            pass
    return path


def main():
    """Run the bundled binary, passing through argv/env/stdio and exit code."""
    binary = get_binary_path()
    args = [binary] + sys.argv[1:]
    if sys.platform == "win32":
        # os.execv on Windows spawns a detached child with broken console/signal
        # semantics — use subprocess and propagate the real exit code instead.
        sys.exit(subprocess.call(args))
    # POSIX: replace this process entirely so the Go server owns the PID, stdio,
    # and signals (SIGINT/SIGTERM go straight to it); env is inherited.
    os.execvp(binary, args)


if __name__ == "__main__":
    main()
'''

MAIN = "from . import main\n\nif __name__ == \"__main__\":\n    main()\n"


def _read_python_src(name: str, src_dir: str) -> bytes:
    """Read a Python source file from src_dir, failing loudly if absent."""
    path = os.path.join(src_dir, name)
    if not os.path.isfile(path):
        raise FileNotFoundError(
            f"required Python source file not found: {path}\n"
            f"  (expected in --python-src={src_dir!r}; "
            "make sure python/web_researcher_mcp/ is populated before building wheels)"
        )
    with open(path, "rb") as f:
        return f.read()


def _record_hash(data: bytes) -> str:
    digest = hashlib.sha256(data).digest()
    return "sha256=" + base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def _find_binary(dist_dir: str, goos: str, goarch: str) -> str:
    """Locate the GoReleaser-built binary for a target, mirroring build-mcpb.sh.

    GoReleaser emits dist/<project>_<os>_<arch>[_v1|_v8.0]/<binary>[.exe]; the
    amd64 dir carries a `_v1` GOAMD64 suffix. Probe the known shapes.
    """
    exe = ".exe" if goos == "windows" else ""
    name = BINARY + exe
    candidates = [
        os.path.join(dist_dir, f"{DIST_NAME}_{goos}_{goarch}_v1", name),
        os.path.join(dist_dir, f"{DIST_NAME}_{goos}_{goarch}", name),
        os.path.join(dist_dir, f"{DIST_NAME}_{goos}_{goarch}_v8.0", name),
    ]
    for c in candidates:
        if os.path.isfile(c):
            return c
    raise FileNotFoundError(
        f"no GoReleaser binary for {goos}/{goarch}; tried:\n  " + "\n  ".join(candidates)
    )


def _metadata(version: str, readme: str) -> str:
    body = [
        "Metadata-Version: 2.1",
        f"Name: {DIST_NAME}",
        f"Version: {version}",
        f"Summary: {SUMMARY}",
        f"Author: {AUTHOR}",
        f"Home-page: {HOMEPAGE}",
        "License: MIT",
        "Project-URL: Source, " + HOMEPAGE,
        "Project-URL: Issues, " + HOMEPAGE + "/issues",
        "Project-URL: Documentation, " + HOMEPAGE + "/blob/main/docs/PYTHON_CLIENT.md",
        "Project-URL: Changelog, " + HOMEPAGE + "/releases",
        f"Requires-Python: {REQUIRES_PYTHON}",
        "Classifier: Development Status :: 5 - Production/Stable",
        "Classifier: Intended Audience :: Developers",
        "Classifier: Intended Audience :: Science/Research",
        "Classifier: License :: OSI Approved :: MIT License",
        "Classifier: Programming Language :: Python :: 3",
        "Classifier: Programming Language :: Python :: 3.10",
        "Classifier: Programming Language :: Python :: 3.11",
        "Classifier: Programming Language :: Python :: 3.12",
        "Classifier: Programming Language :: Python :: 3.13",
        "Classifier: Topic :: Internet :: WWW/HTTP :: Indexing/Search",
        "Classifier: Topic :: Scientific/Engineering :: Information Analysis",
        "Classifier: Topic :: Software Development :: Libraries :: Python Modules",
        "Classifier: Typing :: Typed",
        "Description-Content-Type: text/markdown",
        "",
        readme,
    ]
    return "\n".join(body)


def _wheel_meta(tag: str) -> str:
    return "\n".join(
        [
            "Wheel-Version: 1.0",
            "Generator: web-researcher-mcp build_wheels.py",
            "Root-Is-Purelib: false",
            f"Tag: py3-none-{tag}",
            "",
        ]
    )


def build_wheel(version: str, binary_path: str, goos: str, tag: str, out_dir: str, readme: str, python_src: str) -> str:
    """Assemble one platform wheel and return its path."""
    exe = ".exe" if goos == "windows" else ""
    dist_info = f"{PKG_NAME}-{version}.dist-info"
    with open(binary_path, "rb") as f:
        binary_bytes = f.read()

    shim = SHIM.format(version=version, binary=BINARY, exe=exe)
    files = {
        f"{PKG_NAME}/_shim.py": shim.encode(),
        f"{PKG_NAME}/py.typed": b"",  # PEP 561 — marks the package as typed
        f"{PKG_NAME}/__init__.py": _read_python_src("__init__.py", python_src),
        f"{PKG_NAME}/__main__.py": MAIN.encode(),
        f"{PKG_NAME}/client.py": _read_python_src("client.py", python_src),
        f"{PKG_NAME}/models.py": _read_python_src("models.py", python_src),
        f"{PKG_NAME}/_server.py": _read_python_src("_server.py", python_src),
        f"{dist_info}/METADATA": _metadata(version, readme).encode(),
        f"{dist_info}/WHEEL": _wheel_meta(tag).encode(),
        f"{dist_info}/entry_points.txt": (
            f"[console_scripts]\n{DIST_NAME} = {PKG_NAME}:main\n"
        ).encode(),
    }
    binary_arcname = f"{PKG_NAME}/bin/{BINARY}{exe}"

    # RECORD lists every file with its hash+size; its own row carries empty
    # hash/size (it can't hash itself).
    record = io.StringIO()
    writer = csv.writer(record, lineterminator="\n")
    for arc, data in files.items():
        writer.writerow([arc, _record_hash(data), len(data)])
    writer.writerow([binary_arcname, _record_hash(binary_bytes), len(binary_bytes)])
    writer.writerow([f"{dist_info}/RECORD", "", ""])

    wheel_name = f"{PKG_NAME}-{version}-py3-none-{tag}.whl"
    wheel_path = os.path.join(out_dir, wheel_name)
    with zipfile.ZipFile(wheel_path, "w", zipfile.ZIP_DEFLATED) as zf:
        for arc, data in files.items():
            zf.writestr(zipfile.ZipInfo(arc, date_time=ZIP_EPOCH), data)
        # The binary needs the Unix exec bit (0755) recorded in the zip entry.
        info = zipfile.ZipInfo(binary_arcname, date_time=ZIP_EPOCH)
        info.external_attr = 0o755 << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        zf.writestr(info, binary_bytes)
        zf.writestr(
            zipfile.ZipInfo(f"{dist_info}/RECORD", date_time=ZIP_EPOCH),
            record.getvalue(),
        )
    return wheel_path


def main() -> int:
    ap = argparse.ArgumentParser(description="Build PyPI platform wheels for the Go binary.")
    ap.add_argument("version", help="release version (with or without leading v)")
    ap.add_argument("--dist", default="dist", help="GoReleaser dist dir (default: dist)")
    ap.add_argument("--out", default="dist", help="output dir for wheels (default: dist)")
    ap.add_argument("--readme", default="README.md", help="README for long description")
    ap.add_argument(
        "--python-src",
        default="python/web_researcher_mcp",
        help="directory containing Python source modules to bundle (default: python/web_researcher_mcp)",
    )
    args = ap.parse_args()

    version = args.version.lstrip("v")
    if not _PEP440_RE.match(version):
        print(
            f"error: {version!r} is not a valid PEP 440 version "
            "(expected e.g. 1.25.0); refusing to build wheels.",
            file=sys.stderr,
        )
        return 2
    os.makedirs(args.out, exist_ok=True)
    try:
        with open(args.readme, encoding="utf-8") as f:
            readme = f.read()
    except OSError:
        readme = SUMMARY

    built = []
    for goos, goarch, tags in PLATFORMS:
        binary_path = _find_binary(args.dist, goos, goarch)
        for tag in tags:
            path = build_wheel(version, binary_path, goos, tag, args.out, readme, args.python_src)
            built.append(path)
            print(f"built {os.path.basename(path)}")

    print(f"\n{len(built)} wheels written to {args.out}/")
    return 0


if __name__ == "__main__":
    sys.exit(main())

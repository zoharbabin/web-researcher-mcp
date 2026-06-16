"""
Manages the Go binary subprocess lifecycle for web-researcher-mcp.
"""
from __future__ import annotations

import os
import signal
import socket
import subprocess
import time
import urllib.error
import urllib.request
from typing import Optional


def _find_free_port() -> int:
    """Bind to 127.0.0.1:0, retrieve the OS-assigned port, then release it."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def _wait_for_ready(port: int, timeout: float = 30.0) -> None:
    """
    Poll GET http://127.0.0.1:{port}/health/live every 0.1 s until it returns
    HTTP 200, or raise TimeoutError if *timeout* seconds elapse first.
    """
    url = f"http://127.0.0.1:{port}/health/live"
    deadline = time.monotonic() + timeout

    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1.0) as resp:
                if resp.status == 200:
                    return
        except (urllib.error.URLError, OSError):
            pass
        time.sleep(0.1)

    raise TimeoutError(
        f"web-researcher-mcp did not become ready on port {port} "
        f"within {timeout:.1f} s"
    )


class _ServerProcess:
    """
    Manages a single Go binary subprocess that exposes the MCP HTTP server.

    Usage
    -----
    Either use as a context manager::

        with _ServerProcess() as proc:
            port = proc.port
            ...

    Or call ``start()`` / ``stop()`` manually::

        proc = _ServerProcess()
        port = proc.start()
        ...
        proc.stop()
    """

    def __init__(
        self,
        port: Optional[int] = None,
        binary_path: Optional[str] = None,
        env: Optional[dict] = None,
        startup_timeout: float = 30.0,
    ) -> None:
        self.port: int = port if port is not None else _find_free_port()
        self._binary_path: Optional[str] = binary_path
        self._extra_env: Optional[dict] = env
        self._startup_timeout: float = startup_timeout
        self._proc: Optional[subprocess.Popen] = None  # type: ignore[type-arg]

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def start(self) -> int:
        """
        Start the Go binary subprocess and wait for it to be ready.

        Returns the port the server is listening on.  If the process is
        already running the current port is returned immediately.
        """
        if self._proc is not None and self._proc.poll() is None:
            return self.port

        binary = self._resolve_binary()

        child_env = os.environ.copy()
        if self._extra_env:
            child_env.update(self._extra_env)
        # PORT must override whatever the caller supplied in *env*.
        child_env["PORT"] = str(self.port)

        # No shell=True; args is a list so no injection surface.
        self._proc = subprocess.Popen(
            [binary],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            env=child_env,
        )

        try:
            _wait_for_ready(self.port, timeout=self._startup_timeout)
        except TimeoutError:
            self.stop()
            raise

        return self.port

    def stop(self) -> None:
        """
        Gracefully stop the subprocess.

        Sends SIGTERM and waits up to 5 s; sends SIGKILL if it does not exit
        in time.  No-op when no process has been started.
        """
        if self._proc is None:
            return

        proc = self._proc
        self._proc = None

        if proc.poll() is not None:
            # Already exited.
            return

        proc.terminate()
        try:
            proc.wait(timeout=5.0)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()

    # ------------------------------------------------------------------
    # Context-manager support
    # ------------------------------------------------------------------

    def __enter__(self) -> "_ServerProcess":
        self.start()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb) -> None:
        self.stop()

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _resolve_binary(self) -> str:
        """Return the absolute path to the Go binary."""
        if self._binary_path is not None:
            return self._binary_path

        # Lazy import so this module remains importable even when _shim has
        # not yet been installed (e.g. during build-time introspection).
        from web_researcher_mcp._shim import get_binary_path  # noqa: PLC0415

        return get_binary_path()

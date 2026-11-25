"""A project on the compute-box and the operations it exposes.

Everything underneath is plain HTTP + JSON:

    PUT  /v1/projects/{project}/operations                sync every operation, one call
    POST /v1/projects/{project}/operations/{name}/jobs    run one, progress streamed as SSE
    GET  /v1/machines                                     which local-agents are online
"""
from __future__ import annotations

import os
from dataclasses import asdict, dataclass
from typing import Any, Awaitable, Callable, ParamSpec, TypeVar

import httpx

from sonic_screwdriver import _sse
from sonic_screwdriver.errors import SonicError
from sonic_screwdriver.operation import Operation
from sonic_screwdriver.programs import Program, InstalledProgram

P = ParamSpec("P")
R = TypeVar("R")


# The hosted compute-box, used when neither `url` nor SONIC_URL is set.
DEFAULT_URL = "https://app.sonicscrew.click"


@dataclass(frozen=True)
class Step:
    """One action the agent took, streamed while a job runs."""

    n: int
    description: str


class Sonic:
    """Connection settings for one project, plus the operations declared in it.

    `machine` is the local-agent jobs run on by default. `context` is added to every
    operation's instructions, for things all of them need (such as how to log in).
    `url` and `api_key` default to the SONIC_URL and SONIC_API_KEY environment variables;
    without SONIC_URL, `url` is the hosted compute-box (DEFAULT_URL).
    """

    def __init__(
        self,
        project: str,
        *,
        machine: str | None = None,
        context: str = "",
        programs: dict[str, Program] | None = None,
        url: str | None = None,
        api_key: str | None = None,
    ):
        self.project = project
        self.machine = machine
        self.context = context.strip()
        self.programs = programs
        self.url = url or os.getenv("SONIC_URL") or DEFAULT_URL
        self.api_key = api_key or os.getenv("SONIC_API_KEY")
        self.operations: dict[str, Operation] = {}
        self._http: httpx.AsyncClient | None = None

    def operation(self, fn: Callable[P, Awaitable[R]]) -> Operation[P, R]:
        """Decorator: declare a typed, documented async function as an operation."""
        op = Operation(self, fn)
        if op.name in self.operations:
            raise ValueError(f"operation {op.name!r} is declared twice in {self.project!r}")
        self.operations[op.name] = op
        return op

    def definitions(self) -> list[dict[str, Any]]:
        return [op.definition() for op in self.operations.values()]

    async def sync(self, *, allow_empty: bool = False) -> dict[str, list[str]]:
        """Make the project's operations on the compute-box match the ones declared here.

        Syncing nothing would delete every operation in the project, so it is refused unless
        `allow_empty=True`. It usually means the module declaring them was never imported.
        """
        if not self.operations and not allow_empty:
            raise SonicError("invalid_input", f"no operations declared in {self.project!r}; refusing to sync")
        if self.programs is not None:
            await self._request("PUT", f"/v1/projects/{self.project}/programs",
                                json={name: asdict(p) for name, p in self.programs.items()})
        r = await self._request("PUT", f"/v1/projects/{self.project}/operations", json=self.definitions())
        return r.json()

    async def machines(self) -> list[dict[str, Any]]:
        r = await self._request("GET", "/v1/machines")
        return r.json()["machines"]

    async def run_job(
        self,
        name: str,
        input: dict[str, Any],
        *,
        machine: str | None = None,
        idempotency_key: str | None = None,
        on_step: Callable[[Step], None] | None = None,
    ) -> tuple[Any, str | None]:
        """Start a job and follow its event stream to the end. Returns (output, job id)."""
        machine = machine or self.machine
        if not machine:
            raise SonicError("invalid_input", f"{name}: no machine given and no default set")
        headers = {"Accept": "text/event-stream"}
        if idempotency_key:
            headers["Idempotency-Key"] = idempotency_key
        path = f"/v1/projects/{self.project}/operations/{name}/jobs"
        job_id = None
        try:
            async with self.http.stream("POST", path, json={"machine": machine, "input": input}, headers=headers) as r:
                if r.status_code >= 400:
                    await r.aread()
                    raise SonicError.from_response(r)
                async for event, data in _sse.events(r):
                    job_id = data.get("job_id", job_id)
                    if event == "step" and on_step:
                        on_step(Step(data["n"], data["description"]))
                    elif event == "succeeded":
                        return data["output"], job_id
                    elif event in ("failed", "cancelled"):
                        err = data.get("error") or {"code": event, "message": f"job {event}"}
                        raise SonicError(err["code"], err["message"], job_id)
        except httpx.TransportError as e:
            raise SonicError("unreachable", f"compute-box at {self.url}: {e}", job_id) from e
        raise SonicError("machine_disconnected", "the job's stream ended before it finished", job_id)

    async def aclose(self) -> None:
        if self._http:
            await self._http.aclose()
            self._http = None

    @property
    def http(self) -> httpx.AsyncClient:
        # Created on first use, so it binds to the event loop the app actually runs in.
        if self._http is None:
            self._http = httpx.AsyncClient(
                base_url=self.url,
                headers={"Authorization": f"Bearer {self.api_key}"} if self.api_key else {},
                timeout=httpx.Timeout(10, read=600),  # jobs can go quiet for minutes
            )
        return self._http

    async def _request(self, method: str, path: str, **kwargs: Any) -> httpx.Response:
        try:
            r = await self.http.request(method, path, **kwargs)
        except httpx.TransportError as e:
            raise SonicError("unreachable", f"compute-box at {self.url}: {e}") from e
        if r.status_code >= 400:
            raise SonicError.from_response(r)
        return r


class Machine:
    """Declare installations once per machine, independently of project operations."""

    def __init__(self, name: str, *, programs: dict[str, InstalledProgram],
                 url: str | None = None, api_key: str | None = None):
        self.name = name
        self.programs = programs
        self._client = Sonic(project="", url=url, api_key=api_key)

    async def sync(self, *, allow_empty: bool = False) -> None:
        if not self.programs and not allow_empty:
            raise SonicError("invalid_input", "no machine programs declared; use allow_empty=True to clear")
        await self._client._request("PUT", f"/v1/machines/{self.name}/programs",
                                    json={name: asdict(p) for name, p in self.programs.items()})

    async def aclose(self) -> None:
        await self._client.aclose()

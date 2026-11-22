"""sonic-screwdriver plumbing for FastAPI. Nothing to edit here.

Syncs the machine and project declared in dentasoft.py at startup, closes connections at
shutdown, turns agent failures into HTTP errors, and adds /health.
"""
from contextlib import asynccontextmanager

from fastapi import APIRouter, FastAPI, Request
from fastapi.responses import JSONResponse
from sonic_screwdriver import SonicError

from dentasoft import dental_clinic, front_desk

@asynccontextmanager
async def lifespan(app: FastAPI):
    try:
        await front_desk.sync()
        await dental_clinic.sync()
        yield
    finally:
        await front_desk.aclose()
        await dental_clinic.aclose()


_STATUS = {"invalid_input": 422, "not_found": 404, "unreachable": 503, "machine_offline": 503, "machine_busy": 503}


async def _sonic_error(request: Request, e: SonicError) -> JSONResponse:
    return JSONResponse({"detail": {"code": e.code, "message": e.message}}, status_code=_STATUS.get(e.code, 502))


_health = APIRouter()


@_health.get("/health")
async def health() -> dict:
    online = {m["name"]: m["online"] for m in await dental_clinic.machines()}
    return {"ok": True, "machine_online": online.get(dental_clinic.machine, False)}


def setup(app: FastAPI) -> None:
    """Add sonic error handling and the /health endpoint to the app."""
    app.add_exception_handler(SonicError, _sonic_error)
    app.include_router(_health)

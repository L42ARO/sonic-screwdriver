import httpx


class SonicError(Exception):
    """Anything that went wrong reaching the compute-box or running a job.

    `code` is stable and safe to branch on: unreachable, invalid_input, not_found,
    machine_offline, machine_busy, machine_disconnected, timeout, agent_gave_up,
    output_invalid, cancelled.
    """

    def __init__(self, code: str, message: str, job_id: str | None = None):
        super().__init__(f"{code}: {message}")
        self.code = code
        self.message = message
        self.job_id = job_id

    @classmethod
    def from_response(cls, r: httpx.Response) -> "SonicError":
        """Build from an error response: {"error": {"code": ..., "message": ...}}."""
        try:
            err = r.json()["error"]
            return cls(err["code"], err["message"])
        except Exception:
            return cls(f"http_{r.status_code}", r.text[:300])

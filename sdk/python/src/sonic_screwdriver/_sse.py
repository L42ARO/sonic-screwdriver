"""Server-Sent Events: read a streaming response as (event name, JSON data) pairs."""
import json
from typing import Any, AsyncIterator

import httpx


async def events(r: httpx.Response) -> AsyncIterator[tuple[str, dict[str, Any]]]:
    name, data = "message", []
    async for line in r.aiter_lines():
        if line.startswith("event:"):
            name = line[6:].strip()
        elif line.startswith("data:"):
            data.append(line[5:].lstrip())
        elif line == "" and data:
            yield name, json.loads("\n".join(data))
            name, data = "message", []

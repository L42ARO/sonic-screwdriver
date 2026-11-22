"""An operation: a typed, documented function that the agent carries out.

The function's signature is the input, its return type is the output, and its docstring
is the description (first paragraph) plus the agent's instructions (the rest). The body
is never run.
"""
from __future__ import annotations

import copy
import functools
import inspect
import typing
from typing import TYPE_CHECKING, Any, Awaitable, Callable, Generic, ParamSpec, TypeVar

from pydantic import TypeAdapter, ValidationError, create_model

from sonic_screwdriver.errors import SonicError

if TYPE_CHECKING:
    from sonic_screwdriver.client import Sonic, Step

P = ParamSpec("P")
R = TypeVar("R")


class Operation(Generic[P, R]):
    def __init__(self, sonic: Sonic, fn: Callable[P, Awaitable[R]]):
        functools.update_wrapper(self, fn)
        self.sonic = sonic
        self.name = fn.__name__
        self.description, self.instructions = _read_docstring(fn)
        self.signature = inspect.signature(fn)
        hints = typing.get_type_hints(fn, include_extras=True)
        if "return" not in hints:
            raise TypeError(f"{self.name}: needs a return type, it is the operation's output")
        self.input = create_model(
            f"{self.name}_input",
            **{
                name: (hints[name], ... if p.default is p.empty else p.default)
                for name, p in self.signature.parameters.items()
            },
        )
        self.output = TypeAdapter(hints["return"])
        self.options: dict[str, Any] = {}

    def definition(self) -> dict[str, Any]:
        """The JSON the compute-box stores for this operation."""
        return {
            "name": self.name,
            "description": self.description,
            "instructions": "\n\n".join(filter(None, [self.sonic.context, self.instructions])),
            "input": self.input.model_json_schema(),
            "output": self.output.json_schema(),
        }

    def with_options(
        self,
        *,
        machine: str | None = None,
        idempotency_key: str | None = None,
        on_step: Callable[[Step], None] | None = None,
    ) -> Operation[P, R]:
        """The same operation with per-call options, e.g. an idempotency key for a write."""
        op = copy.copy(self)
        op.options = {"machine": machine, "idempotency_key": idempotency_key, "on_step": on_step}
        return op

    async def __call__(self, *args: P.args, **kwargs: P.kwargs) -> R:
        try:
            arguments = self.signature.bind(*args, **kwargs).arguments
            payload = self.input(**arguments).model_dump(mode="json")
        except (TypeError, ValidationError) as e:
            raise SonicError("invalid_input", f"{self.name}: {e}") from e

        output, job_id = await self.sonic.run_job(self.name, payload, **self.options)

        try:
            return self.output.validate_python(output)
        except ValidationError as e:
            raise SonicError("output_invalid", f"{self.name}: {e}", job_id) from e


def _read_docstring(fn: Callable) -> tuple[str, str]:
    doc = inspect.getdoc(fn)
    if not doc:
        raise TypeError(f"{fn.__name__}: needs a docstring, it is the agent's instructions")
    first, _, rest = doc.partition("\n\n")
    description = " ".join(first.split())
    return description, rest.strip() or description

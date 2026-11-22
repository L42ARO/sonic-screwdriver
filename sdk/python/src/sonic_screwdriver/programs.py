"""Project program guidance and machine-specific installation settings."""
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Program:
    description: str
    instructions: str = ""


@dataclass(frozen=True)
class InstalledProgram:
    executable: str
    window_title_contains: str
    args: list[str] = field(default_factory=list)
    working_directory: str = ""
    startup_timeout_ms: int = 30000

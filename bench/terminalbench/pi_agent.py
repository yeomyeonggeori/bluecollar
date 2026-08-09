"""Terminal-Bench agent that runs pi, so it can be measured on the same row.

pi is installed into the task container and run there, which is the pattern
Terminal-Bench's own npm-based agents use. Nothing here is bluecollar's: it
exists so the comparison holds the benchmark, the model and the task fixed and
changes only the harness.
"""

import json
import os
import shlex
import time
from pathlib import Path

from terminal_bench.agents.base_agent import AgentResult
from terminal_bench.agents.installed_agents.abstract_installed_agent import (
    AbstractInstalledAgent,
)
from terminal_bench.terminal.models import TerminalCommand
from terminal_bench.terminal.tmux_session import TmuxSession

PASSED_THROUGH_KEYS = ("PI_BASE_URL", "PI_MODEL_ID", "PI_API_KEY")


class PiAgent(AbstractInstalledAgent):
    @staticmethod
    def name() -> str:
        return "pi"

    def __init__(self, model_name: str, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._model_name = model_name
        self._version = kwargs.get("version", "latest")

    @property
    def _env(self) -> dict[str, str]:
        return {key: os.environ[key] for key in PASSED_THROUGH_KEYS if key in os.environ}

    @property
    def _install_agent_script_path(self) -> os.PathLike:
        return self._get_templated_script_path("pi-setup.sh.j2")

    def perform_task(
        self,
        instruction: str,
        session: TmuxSession,
        logging_dir: Path | None = None,
    ) -> AgentResult:
        self._agent_started_at = None
        result = super().perform_task(instruction, session, logging_dir)
        self._write_metrics(logging_dir)
        return result

    def _write_metrics(self, logging_dir: Path | None) -> None:
        if logging_dir is None or self._agent_started_at is None:
            return
        logging_dir.mkdir(parents=True, exist_ok=True)
        elapsed_millisecond = round((time.monotonic() - self._agent_started_at) * 1000)
        (logging_dir / "pi-metrics.json").write_text(
            json.dumps({"wallClockMs": elapsed_millisecond, "excludesInstallation": True}, indent=2)
        )

    def _run_agent_commands(self, instruction: str) -> list[TerminalCommand]:
        self._agent_started_at = time.monotonic()
        return [
            TerminalCommand(
                command=(
                    f"pi --provider local --model {shlex.quote(self._model_name)} "
                    f"--api-key {shlex.quote(os.environ.get('PI_API_KEY', 'local'))} "
                    f"-p {shlex.quote(instruction)}"
                ),
                min_timeout_sec=0.0,
                max_timeout_sec=float("inf"),
                block=True,
                append_enter=True,
            ),
        ]

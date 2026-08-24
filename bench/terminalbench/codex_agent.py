"""Terminal-Bench agent that runs Codex, so it can be measured on the same row.

Codex is installed into the task container and run there, the same pattern
pi_agent.py uses. Nothing here is bluecollar's: a second comparison harness is
what turns "bluecollar loses to pi" into a claim about bluecollar rather than a
claim about one other implementation.

The container is the sandbox, which is why the agent runs with its own
sandboxing off. Terminal-Bench builds the container per task and throws it away,
and a harness that stops to ask for approval blocks forever on a benchmark with
nobody to ask.
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

PASSED_THROUGH_KEYS = ("CODEX_BASE_URL", "CODEX_MODEL_ID", "CODEX_API_KEY")


class CodexAgent(AbstractInstalledAgent):
    @staticmethod
    def name() -> str:
        return "codex"

    def __init__(self, model_name: str, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._model_name = model_name
        self._version = kwargs.get("version", "latest")

    @property
    def _env(self) -> dict[str, str]:
        return {key: os.environ[key] for key in PASSED_THROUGH_KEYS if key in os.environ}

    @property
    def _install_agent_script_path(self) -> os.PathLike:
        return self._get_templated_script_path("codex-setup.sh.j2")

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
        (logging_dir / "codex-metrics.json").write_text(
            json.dumps({"wallClockMs": elapsed_millisecond, "excludesInstallation": True}, indent=2)
        )

    def _run_agent_commands(self, instruction: str) -> list[TerminalCommand]:
        self._agent_started_at = time.monotonic()
        return [
            TerminalCommand(
                command=(
                    "codex exec --skip-git-repo-check "
                    "--dangerously-bypass-approvals-and-sandbox "
                    f"--model {shlex.quote(self._model_name)} "
                    f"{shlex.quote(instruction)}"
                ),
                min_timeout_sec=0.0,
                max_timeout_sec=float("inf"),
                block=True,
                append_enter=True,
            ),
        ]

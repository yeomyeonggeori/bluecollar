"""Terminal-Bench agent that runs the bluecollar loop against a task container.

The loop runs on the host and reaches the task through `docker exec`, so the
container stays exactly the environment the benchmark built. Point
`--agent-import-path` at this module's class and give it the same model every
other harness on the row is given.
"""

import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

from terminal_bench.agents.base_agent import AgentResult, BaseAgent
from terminal_bench.agents.failure_mode import FailureMode
from terminal_bench.terminal.tmux_session import TmuxSession

class BluecollarAgent(BaseAgent):
    def __init__(self, model_name: str | None = None, **kwargs):
        super().__init__(**kwargs)
        self._model_name = model_name or os.environ.get("BLUECOLLAR_MODEL", "qwen3:4b")
        self._binary_path = os.environ.get("BLUECOLLAR_BINARY", "bluecollar")
        self._endpoint = os.environ.get(
            "BLUECOLLAR_MODEL_ENDPOINT", "http://127.0.0.1:11434/v1"
        )
        self._timeout_second = int(os.environ.get("BLUECOLLAR_TIMEOUT_SECOND", "900"))
        self._api_key = os.environ.get("BLUECOLLAR_API_KEY", "")

    @staticmethod
    def name() -> str:
        return "bluecollar"

    def perform_task(
        self,
        instruction: str,
        session: TmuxSession,
        logging_dir: Path | None = None,
    ) -> AgentResult:
        binary_path = shutil.which(self._binary_path) or self._binary_path
        if not Path(binary_path).exists():
            return AgentResult(failure_mode=FailureMode.FATAL_LLM_ERROR)

        metrics_path = Path(tempfile.mkdtemp()) / "metrics.json"
        completed = subprocess.run(
            [
                binary_path,
                "--metrics",
                str(metrics_path),
                "--model",
                self._model_name,
                "--endpoint",
                self._endpoint,
                "--api-key",
                self._api_key,
                "--exec-prefix",
                f"docker exec -i {session.container.name}",
                "--timeout",
                f"{self._timeout_second}s",
                self._render_instruction(instruction),
            ],
            capture_output=True,
            text=True,
            errors="replace",
            timeout=self._timeout_second + 60,
        )
        metrics = self._read_metrics(metrics_path)
        self._write_logs(logging_dir, completed, metrics)
        return AgentResult(
            total_input_tokens=metrics.get("promptTokens", 0),
            total_output_tokens=metrics.get("completionTokens", 0),
            failure_mode=FailureMode.NONE
            if completed.returncode == 0
            else FailureMode.UNKNOWN_AGENT_ERROR,
        )

    def _read_metrics(self, metrics_path: Path) -> dict:
        if not metrics_path.exists():
            return {}
        return json.loads(metrics_path.read_text())

    def _write_logs(self, logging_dir: Path | None, completed, metrics: dict) -> None:
        if logging_dir is None:
            return
        logging_dir.mkdir(parents=True, exist_ok=True)
        (logging_dir / "bluecollar-ledger.txt").write_text(completed.stderr or "")
        (logging_dir / "bluecollar-metrics.json").write_text(json.dumps(metrics, indent=2))

import json
import os
import subprocess
from dataclasses import dataclass
from typing import Any, Dict, List, Optional


class CloudClawError(RuntimeError):
    pass


@dataclass
class Client:
    binary: str = "cloudclaw"
    data_dir: str = "./cloudclaw_data/data"
    db_driver: str = "postgres"
    db_dsn: str = ""

    def submit_task(self, user_id: str, task_type: str, input_text: str, max_retries: int = 2) -> Dict[str, Any]:
        return self._run_json(
            [
                "task",
                "submit",
                "--user-id",
                user_id,
                "--task-type",
                task_type,
                "--input",
                input_text,
                "--max-retries",
                str(max_retries),
            ]
        )

    def get_task_status(self, task_id: str) -> Dict[str, Any]:
        return self._run_json(["task", "status", "--task-id", task_id])

    def cancel_task(self, task_id: str) -> Dict[str, Any]:
        return self._run_json(["task", "cancel", "--task-id", task_id])

    def get_container_status(self) -> List[Dict[str, Any]]:
        return self._run_json(["container-status"])

    def get_queue_length(self) -> Dict[str, Any]:
        return self._run_json(["queue-length"])

    def get_audit_events(self, task_id: Optional[str] = None) -> List[Dict[str, Any]]:
        args = ["audit"]
        if task_id:
            args.extend(["--task-id", task_id])
        return self._run_json(args)

    def _run_json(self, subcmd: List[str]) -> Any:
        driver = (self.db_driver or "postgres").strip().lower()
        if driver == "postgresql":
            driver = "postgres"
        if driver != "postgres":
            raise CloudClawError(f"unsupported db_driver: {self.db_driver} (only postgres is supported)")
        dsn = (self.db_dsn or os.environ.get("DB_DSN", "")).strip()
        if not dsn:
            raise CloudClawError("db_dsn is required when db_driver=postgres")

        args = [
            self.binary,
            *subcmd,
            "--data-dir",
            self.data_dir,
            "--db-driver",
            driver,
            "--db-dsn",
            dsn,
        ]

        proc = subprocess.run(args, capture_output=True, text=True)
        if proc.returncode != 0:
            raise CloudClawError(proc.stderr.strip() or proc.stdout.strip() or "cloudclaw command failed")
        try:
            return json.loads(proc.stdout)
        except json.JSONDecodeError as exc:
            raise CloudClawError(f"invalid json output: {exc}") from exc

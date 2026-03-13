import json
import pathlib
import sys
import unittest
from unittest.mock import patch

SDK_ROOT = pathlib.Path(__file__).resolve().parents[1]
if str(SDK_ROOT) not in sys.path:
    sys.path.insert(0, str(SDK_ROOT))

from cloudclaw.client import Client, CloudClawError


class _Proc:
    def __init__(self, returncode=0, stdout="", stderr=""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


class ClientTests(unittest.TestCase):
    @patch("cloudclaw.client.subprocess.run")
    def test_submit_task_builds_command(self, mock_run):
        mock_run.return_value = _Proc(stdout=json.dumps({"id": "tsk_1"}))
        cli = Client(
            binary="cloudclaw",
            data_dir="/tmp/data",
            db_driver="postgres",
            db_dsn="postgres://u:p@127.0.0.1:15432/cloudclaw?sslmode=disable",
        )

        task = cli.submit_task("u1", "search", "hello", max_retries=3)
        self.assertEqual(task["id"], "tsk_1")

        args = mock_run.call_args[0][0]
        self.assertEqual(args[0], "cloudclaw")
        self.assertIn("--data-dir", args)
        self.assertIn("/tmp/data", args)
        self.assertIn("--max-retries", args)
        self.assertIn("3", args)

    @patch("cloudclaw.client.subprocess.run")
    def test_run_json_raises_on_nonzero_exit(self, mock_run):
        mock_run.return_value = _Proc(returncode=1, stderr="boom")
        cli = Client(db_dsn="postgres://u:p@127.0.0.1:15432/cloudclaw?sslmode=disable")
        with self.assertRaises(CloudClawError):
            cli.get_queue_length()

    @patch("cloudclaw.client.subprocess.run")
    def test_run_json_raises_on_invalid_json(self, mock_run):
        mock_run.return_value = _Proc(stdout="not-json")
        cli = Client(db_dsn="postgres://u:p@127.0.0.1:15432/cloudclaw?sslmode=disable")
        with self.assertRaises(CloudClawError):
            cli.get_queue_length()


if __name__ == "__main__":
    unittest.main()

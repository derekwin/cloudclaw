# cloudclaw-sdk (Python)

Thin Python SDK for CloudClaw CLI.

## Install

```bash
pip install -e ./sdk/python
```

## Usage

```python
from cloudclaw import Client

client = Client(binary="cloudclaw", data_dir="./cloudclaw_data/data", db_driver="sqlite")

task = client.submit_task(user_id="u1", task_type="search", input_text="hello")
status = client.get_task_status(task["id"])
print(status["status"])
```

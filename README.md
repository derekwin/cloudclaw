# cloudclaw

```bash
cd cloudclaw
export AGENT_RUNTIME=opencode
export OPENCODE_CONFIG_FILE="$HOME/.config/opencode/opencode.json"
bash deploy/server/cloudclawctl.sh up
```

## Common Commands

```bash
# status / logs
bash deploy/server/cloudclawctl.sh status
bash deploy/server/cloudclawctl.sh runner logs 200

# smoke
bash deploy/server/cloudclawctl.sh smoke

# stop
bash deploy/server/cloudclawctl.sh down
```

## Config

```bash
# show config path/content
bash deploy/server/cloudclawctl.sh config path
bash deploy/server/cloudclawctl.sh config show

# import config
bash deploy/server/cloudclawctl.sh config import /abs/path/opencode.json
```

Notes:
- `AGENT_RUNTIME` required: `opencode | claudecode`
- `opencode` uses `OPENCODE_CONFIG_FILE` (default `~/.config/opencode/opencode.json`)
- `claudecode` can bootstrap config with `AGENT_RUNTIME=claudecode bash deploy/server/cloudclawctl.sh config init-full`

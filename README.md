# cloudclaw

```bash
cd cloudclaw
export AGENT_RUNTIME=opencode
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
- `opencode` uses `OPENCODE_CONFIG_FILE` (default `./cloudclaw_data/opencode/config/opencode.json`)
- `opencode` per-user private runtime state is stored on host at `./cloudclaw_data/user-runtime/<user_id>/opencode-home`
- `opencode` `OPENCODE_PERSIST_MODE=auto` by default: if private runtime path is mounted, keep full runtime state there; fallback to minimal pruning only when using in-workspace home
- `opencode` runner defaults to `--workspace-state-mode=ephemeral` (no per-user workspace DB restore/persist)
- `claudecode` can bootstrap config with `AGENT_RUNTIME=claudecode bash deploy/server/cloudclawctl.sh config init-full`
- opencode shared (all containers): `./cloudclaw_data/opencode/config/*` (mounted read-only to `/workspace/.config/opencode`)
- clean historical opencode runtime rows in DB: `bash deploy/server/cloudclawctl.sh db prune-opencode-runtime`

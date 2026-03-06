# cloudclaw

## Quick Start

```bash
cd cloudclaw

# 1) generate full picoclaw config template (first time)
bash deploy/server/cloudclawctl.sh init

# 2) edit config (model_list/tools/mcp/skills...)
bash deploy/server/cloudclawctl.sh config edit

# 3) start everything (install + pool + runner)
bash deploy/server/cloudclawctl.sh up

# 4) verify
bash deploy/server/cloudclawctl.sh smoke
```

## Day-2 Commands

```bash
# status / logs
bash deploy/server/cloudclawctl.sh status
bash deploy/server/cloudclawctl.sh runner logs 200

# update config then restart pool
bash deploy/server/cloudclawctl.sh config edit
bash deploy/server/cloudclawctl.sh up

# stop
bash deploy/server/cloudclawctl.sh down
```

## Config File

```bash
# host path
bash deploy/server/cloudclawctl.sh config path

# show current content
bash deploy/server/cloudclawctl.sh config show

# import an existing full config
bash deploy/server/cloudclawctl.sh config import /abs/path/picoclaw-config.json
```

CloudClaw only reads this shared `config.json` at runtime.
`up` will auto-run `init` when the config file does not exist.

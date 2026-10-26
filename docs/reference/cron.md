# Scheduled Tasks and Cron Jobs

> Back to [README](../README.md)

PicoClaw stores scheduled jobs in the current workspace and can run them either as reminders, full agent turns, or shell commands.

## Schedule Types

PicoClaw currently uses three schedule forms in the cron tool:

- `at_seconds`: one-time job, relative to now. After it runs, the job is removed from the store.
- `every_seconds`: recurring interval, in seconds.
- `cron_expr`: recurring cron expression such as `0 9 * * *`.

The CLI command `picoclaw cron add` supports recurring jobs only:

- `--every <seconds>`
- `--cron '<expr>'`

There is no CLI flag for a one-time `at` job today. `--channel` and `--to` are required so the job has a delivery target. Add `--deliver` to publish the message directly when due; otherwise the message runs through the agent loop.

Examples:

```bash
picoclaw cron add --name "Daily summary" --message "Summarize today's logs" --cron "0 18 * * *" --channel telegram --to 1234567890
picoclaw cron add --name "Ping" --message "heartbeat" --every 300 --channel telegram --to 1234567890 --deliver
```

## Agent Tool Actions

The agent-facing `cron` tool supports these actions:

- `add`: create a new job.
- `list`: show accessible job names, ids, and schedules.
- `get`: fetch one accessible persisted job by `job_id`, including its saved payload.
- `update`: partially update one accessible job by `job_id`; omitted fields are preserved.
- `remove`, `enable`, `disable`: existing management actions.

When rescheduling an existing task, use `list -> get -> update`. Do not use
`remove -> add` just to change the schedule, because recreating a job can drop
the original prompt, delivery target, or command payload.

Remote channel access is scoped to the current `channel/chat_id`: remote callers
can only list, get, or update jobs whose saved `payload.channel` and `payload.to`
match the current conversation. Command jobs include a shell command payload, so
they can only be listed, inspected, or updated from internal channels or remote
channels allowed by `tools.cron.command_allowed_remotes`.

Example tool calls:

```json
{"action":"get","job_id":"79095b2f5685a0f2"}
```

```json
{"action":"update","job_id":"79095b2f5685a0f2","cron_expr":"30 10 * * *"}
```

`update` accepts `name`, `message`, `command`, `deliver`, and exactly one schedule field
(`at_seconds`, `every_seconds`, or `cron_expr`).
Omit `command` to preserve it, set `command` to a non-empty string to replace
it, or set `command` to `""` to clear it. Command updates require the same
channel allowlist and confirmation gates as command creation. Command jobs
always run in command mode and ignore `deliver`.

## Execution Modes

Jobs are stored with a message payload and can execute in three stable user-facing modes:

### `deliver: false`

This is the default for the cron tool.

When the job fires, PicoClaw sends the saved message back through the agent loop as a new agent turn. Use this for scheduled work that may need reasoning, tools, or a generated reply.

### `deliver: true`

When the job fires, PicoClaw publishes the saved message directly to the target channel and recipient without agent processing.

The CLI `picoclaw cron add --deliver` flag uses this mode.

### `command`

When a cron-tool job includes `command`, PicoClaw runs that shell command through the `exec` tool and publishes the command output back to the channel.

For command jobs, `deliver` is forced to `false` when the job is created. The saved `message` becomes descriptive text only; the scheduled action is the shell command.

The current CLI `picoclaw cron add` command does not expose a `command` flag.

## Config and Security Gates

### `tools.cron`

`tools.cron.enabled` controls whether the agent-facing `cron` tool is registered. Default: `true`.

If you disable `tools.cron`, users can no longer create or manage jobs through the agent tool. Already-persisted jobs still run, including agent turns, direct deliveries, and command jobs. The CLI still uses the same job store.

`tools.cron.exec_timeout_minutes` sets the timeout used for scheduled command execution and agent turns. Default: `5`. Set `0` for no timeout.

### `tools.exec`

Scheduled command jobs depend on `tools.exec.enabled`. Default: `true`.

If `tools.exec.enabled` is `false`:

- new command jobs are rejected by the cron tool
- existing command jobs publish a `command execution is disabled` error when they fire

`tools.exec.allow_remote` is still enforced by the exec tool, but cron command scheduling has its own channel gate when the job is created. In practice, reminder jobs can be scheduled from remote channels, while scheduled command jobs are limited to internal channels and configured remote channels.

### `allow_command`

`tools.cron.allow_command` defaults to `true`.

This is not a hard disable switch. If you set `allow_command` to `false`, PicoClaw still allows a command job when the caller explicitly passes `command_confirm: true`.

Command jobs also require either an internal channel or a remote channel allowed by `tools.cron.command_allowed_remotes`. Non-command reminders do not have that restriction.

### `command_allowed_remotes`

`tools.cron.command_allowed_remotes` defaults to an empty list. With the default empty list, remote channels cannot schedule command jobs.

Entries can be either a channel name or a channel plus chat id:

- `telegram` allows command jobs from any Telegram chat.
- `telegram:1234567890` allows command jobs only from that exact Telegram chat id.
- `*` allows command jobs from every non-empty channel.

Warning: `*` is potentially dangerous because any remote channel that can talk
to PicoClaw can schedule shell commands. Use it only when every enabled remote
channel and chat is trusted to request command execution.

This setting only controls the remote-channel gate. It does not bypass `tools.cron.allow_command`, `command_confirm`, `tools.exec.enabled`, or the exec tool's command safety checks.

Example:

```json
{
  "tools": {
    "cron": {
      "enabled": true,
      "exec_timeout_minutes": 5,
      "allow_command": true,
      "command_allowed_remotes": [
        "telegram:1234567890"
      ]
    },
    "exec": {
      "enabled": true
    }
  }
}
```

## Persistence and Location

Cron jobs are stored in:

```text
<workspace>/cron/jobs.json
```

By default, the workspace is:

```text
~/.picoclaw/workspace
```

If `PICOCLAW_HOME` is set, the default workspace becomes:

```text
$PICOCLAW_HOME/workspace
```

Both the gateway and `picoclaw cron` CLI subcommands use the same `cron/jobs.json` file. A running gateway reloads external changes within about one second, so CLI add/remove/enable/disable changes take effect without a restart.

Notes:

- one-time `at_seconds` jobs are deleted after a successful run; failed one-time jobs are kept disabled with `lastError` so they can be inspected or retried
- an overdue one-time job runs once immediately after the gateway starts
- recurring jobs stay in the store until removed
- disabled jobs stay in the store and still appear in `picoclaw cron list`
- job state records `lastStatus`, `lastError`, and the next/last run timestamps; execution failures are no longer reported as success

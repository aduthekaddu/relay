---
title: Schedules
description: Give your agents a night shift. Run agent prompts or commands on a schedule, get notified when they finish, and read the results in the morning.
---

**Schedules** run things for you at set times: an agent with a prompt
("update the dependencies and open a PR"), or a plain command (`make
nightly`). Each run happens in its own terminal on the machine, so you
can watch it live or read its output later. You can be notified when it
succeeds or fails. It is the easiest way to give your agents a night
shift.

## Create a schedule

1. Open **Schedules** (or type `new schedule` in the command center) and
   select **New schedule**.
2. **Name:** for example "Nightly dependency check".
3. **When:** pick a preset (every hour, every night at 02:00, every weekday
   morning, every Monday) or type a cron expression. Relay shows the
   schedule in words, for example *"Every weekday at 02:00"*, and the next
   run time.
4. **Time zone:** defaults to your machine's time zone.
5. **Workspace:** the folder the run starts in.
6. **What to run:**
   - **Agent:** pick an installed agent and write the prompt. The agent runs
     headless (for example `claude -p` or `codex exec`), so it works without
     anyone at the keyboard. Write the prompt as a complete instruction,
     because the agent cannot ask you questions.
   - **Command:** the program and its arguments, for example `make
     nightly`.
7. **Notify when finished:** on by default.
8. Select **Save**. The schedule is enabled at once.

Select **Run now** to test it immediately.

## Write cron expressions

Cron uses five fields: *minute hour day-of-month month day-of-week*.

| Expression | Meaning |
| --- | --- |
| `0 2 * * *` | Every day at 02:00 |
| `0 2 * * 1-5` | Every weekday at 02:00 |
| `*/30 * * * *` | Every 30 minutes |
| `0 9 * * 1` | Every Monday at 09:00 |
| `0 0 1 * *` | The first day of every month at midnight |
| `@hourly`, `@daily`, `@weekly`, `@monthly` | Shortcuts for the obvious times |

## Read the results

Each schedule keeps a history of its runs, with:

- **status**: running, ok, failed, or skipped (a run is skipped when the
  previous run of the same schedule is still going),
- start and end times, and the exit code,
- the **last lines of output**, and a link to the full terminal, including
  its [recording](terminal.md#record-and-replay-sessions) for agent runs.

With **Notify when finished** turned on, you get a *schedule* notification
that says whether the run succeeded, and links to the output.

## Limits and good practice

- A run is stopped after **2 hours**, so a stuck job cannot run forever.
- Runs never overlap: if the previous run is still going, the new one is
  skipped and recorded as *skipped*.
- The machine must be on, and Relay must be running, at the scheduled time.
  Runs missed while the machine was off are not made up later.
- Agents run with the permissions you give them. For unattended runs, work
  in a [git worktree](agents.md#run-agents-in-parallel-with-worktrees)
  and review the changes in the morning, rather than letting an agent push
  to your main branch.
- Headless agent runs use your agent subscription or API credits, the same
  as runs you start yourself.

To turn off the scheduler completely, set
[`schedules.enabled`](../reference/configuration.md#schedules) to `false`.

## Next steps

- [Agents → Review what an agent changed](agents.md#review-what-an-agent-changed)
- [Notifications](notifications.md)
- [Command center → script commands](command-center.md#add-your-own-script-commands)

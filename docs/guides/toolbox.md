---
title: Toolbox
description: Install agent CLIs, runtimes, Chrome, Blender and command-line essentials with one tap, and connect MCP servers to every agent.
---

The **Toolbox** is a catalogue of tools that are useful on a Relay machine.
It shows what is installed and at which version, and it installs anything
missing with one tap. Every install runs in a terminal you can watch, so
nothing happens out of sight. The Toolbox also connects **MCP servers**,
which are plug-ins that give agents new abilities such as driving a
browser, to all your agents at once.

## What's in the catalogue

| Category | Tools |
| --- | --- |
| **Agents** | Claude Code, Codex, Gemini CLI, OpenCode, Kiro CLI, Cursor Agent, Amp, GitHub Copilot CLI, Aider, Qwen Code, Crush |
| **Runtimes** | Node.js, Python with uv, Go, Rust, Bun |
| **Browsers** | Google Chrome, or Chromium where Chrome is unavailable (for example on arm64) |
| **Desktop** | TigerVNC, Openbox, tint2, xclip, xdotool (for [Desktop](desktop.md)) |
| **Creative** | Blender, ffmpeg, ImageMagick |
| **CLI essentials** | ripgrep, fd, fzf, jq, gh, lazygit, btop, neovim, tmux, just, zoxide, bat, eza |
| **Editors** | code-server (for [Code](code.md)) |

Each entry shows a description, a link to the tool's homepage, the
approximate download size, whether it needs `sudo`, and which platforms it
supports.

## Install a tool

1. Open **Toolbox** and find the tool. Search works too.
2. Select **Install**. A terminal opens and runs the tool's official
   install method (for example `apt`, the vendor's installer, or `npm`).
3. If the install needs `sudo`, type your password in the terminal when it
   asks.
4. When it finishes, the entry shows **Installed** and the version.

Install scripts are part of Relay and can be read on GitHub under
`internal/toolbox/recipes/`. Relay runs them exactly as shown there.

:::caution
Installing software runs code on your machine with your permissions, and
with root permissions if you enter your `sudo` password. Only install what
you need. Each recipe uses the tool's official source.
:::

After you install an agent, sign in to it once in a terminal. Most agents
print a link to open in the browser, or ask for an API key. The agent then
appears under **Agents**.

## Connect MCP servers to your agents

MCP (Model Context Protocol) servers give an agent extra tools. The
Toolbox includes:

| Server | Gives agents | Runs as |
| --- | --- | --- |
| **Playwright** | A real browser to open pages, click and take screenshots | `npx @playwright/mcp@latest` |
| **Chrome DevTools** | Control and inspect Chrome: console, network, performance | `npx chrome-devtools-mcp@latest` |
| **Blender** | Control Blender: build and render scenes | `uvx blender-mcp` |
| **Context7** | Up-to-date library documentation | From its npm package |
| **Filesystem** | Controlled file access in chosen folders | From its npm package |

**Toolbox → MCP servers** shows a grid of servers × agents. A tick means
the server is configured for that agent.

1. Select a cell (or **Apply to all** on a server's row).
2. Relay adds the server using the agent's own command where there is one
   (for example `claude mcp add -s user …` or `codex mcp add …`). Otherwise
   it edits the agent's config file, after making a backup:

   | Agent | Config file |
   | --- | --- |
   | Claude Code | `~/.claude.json` |
   | Codex | `~/.codex/config.toml` |
   | Gemini CLI | `~/.gemini/settings.json` |
   | OpenCode | OpenCode's config file |
   | Kiro CLI | `~/.kiro/settings/mcp.json` |
   | Cursor Agent | `~/.cursor/mcp.json` |

3. Start a **new** agent session. Running sessions don't pick up MCP
   changes.

Removing a server works the same way. Browser servers work best while the
[desktop](desktop.md#let-agents-use-the-desktop) is running, so you can
watch what the agent does.

## Next steps

- [Agents](agents.md)
- [Desktop](desktop.md)
- [Contributing a toolbox recipe](https://github.com/aduthekaddu/relay/blob/main/CONTRIBUTING.md#add-a-toolbox-recipe)

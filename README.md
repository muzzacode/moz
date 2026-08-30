<!--
Moz is a personal, model-agnostic, agentic terminal.
It runs locally-first with Ollama and can promote any task to frontier cloud models.
-->

# Moz

Moz is a personal, model-agnostic, agentic terminal. It runs on top of the [PAIEP](https://github.com/muzzacode/paiep) local-AI engineering platform and can promote any task to frontier open-weight models or Claude.

- Local-first: uses PAIEP's Ollama runtime for fast, daily work.
- Model-agnostic: switch between local models and cloud APIs at runtime.
- Cross-workstation memory: event-sourced, encrypted git+age sync.
- Beautiful TUI from day one.
- Future: web dashboard, whisper-style voice commands.

## Requirements

- macOS or Linux
- Go 1.27+
- Ollama (via PAIEP or standalone)
- Git

## Quick start

```bash
# 1. Clone and install
git clone https://github.com/muzzacode/moz.git
cd moz
./install.sh

# 2. Make sure Ollama is running and qwen2.5-coder:14b is installed
#    If you use PAIEP: cd /path/to/paiep && make start

# 3. Run Moz
moz
```

## Bootstrap

If you do not have Go or Ollama installed:

```bash
./bootstrap.sh
```

This installs the required foundation tooling without changing global system defaults.

## Usage

```bash
moz                          # start interactive TUI
moz --model qwen2.5-coder:14b
moz --model claude-sonnet-5  # requires ANTHROPIC_API_KEY + ANTHROPIC_WORKSPACE_ID
moz --resume latest          # continue the most recent saved TUI session
moz --resume <session-id>
moz --help

# run one task in headless mode
moz --model openrouter-default --task "add a help target to the Makefile" --yes

# install to ~/.local/bin
make install

# shell completion
source <(moz completion bash)   # bash
moz completion zsh > "${fpath[1]}/_moz"  # zsh
```

In the TUI:

- Type a message and press `Enter` to chat.
- `Shift+Enter` — insert a newline.
- `/models` — list profiles and API key availability.
- `/mode adaptive` — let Moz pick the model per task.
- `/mode manual` — lock the current model.
- `/mode <profile-id>` or `/model <id>` — switch model manually.
- `/agent on | off` — enable or disable the agent loop.
- `/read <path>` — show file contents.
- `/list [path]` — list directory contents.
- `/grep <pattern> [path]` — search for a pattern.
- `/run <command>` — run a shell command (asks for approval by default).
- `/write <path> <content>` — write a file (asks for approval).
- `/edit <path> <old> -> <new>` — replace text in a file (asks for approval).
- `/git status | diff | commit <msg>` — run git commands.
- `/fetch <url>` — read a web page.
- `/todo [add|done|remove|clear|list]` — manage session todos.
- `/memory` — show current session memory summary.
- `/sessions` — list saved sessions newest-first.
- `/resume [latest|session-id]` — continue a saved conversation.
- `/new` or `/clear` — save the current conversation and start a new session.
- `/undo` — reverse every file change made by the last task.
- `/exit` — quit.

Press `Esc` while a task is running to interrupt it. The conversation is kept, so you can correct course and continue instead of losing the session.

### Agent behaviour

When the agent is on, Moz decides when to call tools. It can also `web_search` via DuckDuckGo when it needs external information.

- **Diff previews.** `edit_file` and `write_file` show a line-numbered preview of the real change before you approve it, and call out ambiguous or failing edits up front.
- **Self-verification.** After the agent edits files, Moz runs the project's own verification command and hands any failure back to the model to fix. Detection prefers `make ci`/`check`/`verify`, then `make build`+`test`, then Go, Cargo, npm, or pytest.
- **Context compaction.** Long sessions are summarized automatically so history stays inside the model's context window instead of overflowing it.
- **Undo.** Every file the agent touches is snapshotted first, so `/undo` restores the previous state byte for byte, including file permissions.
- **Native tool calling.** Providers that support structured tool calls use them directly; only models without native support are taught a text protocol.
- **Automatic retries.** Rate limits and transient provider failures are retried with exponential backoff and jitter instead of losing the task.

### Modes

- **adaptive** (default): classifies the task and picks the cheapest capable model, falling back to local models when cloud keys are missing.
- **manual**: stays on the model you selected.

## Configuration

Moz reads `~/.config/moz/config.yaml` and model profiles from `~/.config/moz/models.yaml`. A default set is created on first run.

```yaml
# ~/.config/moz/config.yaml
ollama_base_url: http://127.0.0.1:11434
memory_dir: ~/.config/moz/memory
agent: false
approval:
  read: always
  write: ask
  edit: ask
  exec: ask
  git: always
agent_options:
  max_turns: 40              # tool-call budget per task
  request_timeout_seconds: 300
  verify: true               # run the project's checks after edits
  verify_command: ""         # override auto-detection
```

Set cloud API keys via environment or the TUI:

```bash
export ANTHROPIC_API_KEY=...
export OPENAI_API_KEY=...
export OPENROUTER_API_KEY=...
```

Or inside Moz:

```
/set ANTHROPIC_API_KEY your-key
```

## Project status

Working: adaptive routing, agent loop with planning and todos, context compaction, interruptible tasks, self-verification, diff previews, session persistence, headless `--task` mode, shell completion, and live cost estimates.

See the architecture and roadmap in Notion:
https://app.notion.com/p/3cbd7006d32681478748e7f162968d5e

## License

MIT

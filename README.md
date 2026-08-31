<!--
Moz is a personal, model-agnostic, agentic terminal.
It runs locally-first with Ollama and can promote any task to frontier cloud models.
-->

# Moz

Moz is a personal, model-agnostic, agentic terminal. It runs on top of the [PAIEP](https://github.com/muzzacode/paiep) local-AI engineering platform and can promote any task to frontier open-weight models or Claude.

- Local-first: routes to local Ollama models by default and only pays for inference when the task warrants it.
- Model-agnostic: local, cheap cloud, and frontier models behind one interface, switchable at runtime.
- Cost-aware: tiered routing, escalation on failure, live spend display, and an optional session budget ceiling.
- Session memory: conversations are saved locally and can be listed and resumed.
- Not yet built: cross-machine memory sync, web dashboard, voice input.

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
- **Repository-aware search.** `find_files` locates files by name or glob, `outline` lists a file's declarations so a large file can be understood without reading it, and `grep` skips ignored directories and binary files with capped results. On an 18,000-file repo this avoids scanning the 97% that lives in `.git`, `node_modules`, and `target`.
- **Project instructions.** If the repo has an `AGENTS.md` (or `.mozrules`, `CLAUDE.md`, `.cursorrules`, `CONVENTIONS.md`), Moz loads it and follows it in preference to its own defaults.
- **Commands that leave the project are flagged.** File tools are sandboxed to the workspace, but `exec` runs a real shell. Commands touching system directories, installing global packages, editing shell config, or force-pushing are called out in the approval prompt, and refused outright under `--yes` where nobody is there to ask.
- **Parallel sub-agents.** For tasks needing several independent questions answered, the agent can run up to 6 investigations in parallel (3 at a time). Each sub-agent has its own context, so large amounts of reading never reach the main conversation. Sub-agents are **read-only by design**: several agents writing at once would corrupt files, and parallel approval prompts are unusable in a terminal.

### Modes

- **adaptive** (default): classifies the task and picks the cheapest model that suits it.
- **manual**: stays on the model you selected.

### How adaptive routing spends money

Each task is scored for difficulty, which sets the **minimum** cost tier worth using. A cheap model failing a hard task costs more in wasted turns than routing it correctly once.

| Task difficulty | Tier used | Cost per 1M in/out |
| --- | --- | --- |
| Below `cloud_threshold` | local Ollama | free |
| Above `cloud_threshold` | cheap cloud | $0.03–0.25 |
| Above `premium_threshold` | frontier | $1.40–25 |

The lineup is picked on measured capability per dollar, not brand:

| Profile | Model | In/Out per 1M | Benchmarks (intel/code/agent) |
| --- | --- | --- | --- |
| `local-coder` | Qwen2.5 Coder 14B (Ollama) | free | — |
| `qwen-flash` | `qwen/qwen3.7-flash` | $0.03 / $0.13 | 1M context, vision |
| `deepseek-flash` | `deepseek/deepseek-v4-flash-0731` | $0.065 / $0.18 | 51.8 / 69.1 / 48.4 |
| **`glm-flash`** | **`z-ai/glm-5.3-flash`** | **$0.075 / $0.25** | **57.5 / 71.5 / 58.2** |
| `glm-5.3` | `z-ai/glm-5.3` | $1.40 / $4.40 | 59.5 / 74.8 / 59.1 |
| `grok-4.6` | `x-ai/grok-4.6` | $2.00 / $6.00 | 60.9 / 76.8 / 58.7 |
| `claude-opus-5` | `anthropic/claude-opus-5` | $5.00 / $25.00 | 63.1 / 78.0 / 59.2 |

GLM 5.3 Flash is the default workhorse because it benchmarks **above Claude Sonnet 5** (55.3 intelligence at $2/$10) while costing about 27x less. Paying frontier prices for ordinary coding is not a quality decision, it is an unexamined one.

Every paid model is reached through OpenRouter, so a single key covers all three tiers. Direct Anthropic and OpenAI profiles exist for `--model` but are deliberately kept out of the stacks, so routing never depends on a second billing relationship being active.

Four things keep the bill down:

- **`prefer_local`** (default on) tries local first even for cloud-worthy work, relying on escalation if it struggles.
- **Escalation**: if a model errors out or returns nothing usable, Moz retries once on the next tier up rather than failing. Local handles the bulk; cloud rescues the remainder.
- **`max_session_cost`**: once spend reaches the ceiling, routing stops choosing paid models. Unset by default, since a hard stop mid-task can be worse than a bill.
- **Health checks**: a local model is only offered if Ollama is actually reachable, so adaptive mode falls back to cloud instead of failing the turn.

Cost preference is derived from each profile's `cost_tier`, not from the order of `models.yaml`, so a mislabelled or mis-ordered profile cannot make a paid model win over a free one.

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
adaptive:
  prefer_local: true         # try local first, escalate if it struggles
  cloud_threshold: 0.5       # task confidence needed to leave local
  premium_threshold: 0.8     # task confidence needed for a frontier model
  max_session_cost: 0        # USD ceiling per session; 0 means unlimited
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

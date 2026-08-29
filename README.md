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
moz --help
```

In the TUI:

- Type a message and press `Enter` to chat.
- `/model <id>` — switch model.
- `/exit` — quit.
- `/memory` — show current session memory summary.

## Configuration

Moz reads `~/.config/moz/config.yaml` and model profiles from `~/.config/moz/models.yaml`. A default set is created on first run.

```yaml
# ~/.config/moz/config.yaml
ollama_base_url: http://127.0.0.1:11434
memory_dir: ~/.config/moz/memory
```

## Project status

Phase 1 in progress: TUI chat skeleton with Ollama, model switching, and session memory.

See the architecture and roadmap in Notion:
https://app.notion.com/p/3cbd7006d32681478748e7f162968d5e

## License

MIT

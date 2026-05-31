# CLI Reference

## Usage

```bash
ccx-agents <command> [options]
```

## Commands

| Command | Description |
|:--------|:------------|
| `init [dir]` | Scaffold a new ccx-agents project |
| `check` | Verify current setup is valid |
| `doctor` | Run system diagnostics |
| `version` | Show version |
| `completion [shell]` | Generate shell completion script |

## Examples

```bash
# Create a new project
ccx-agents init my-ccx-project

# Check current environment
ccx-agents doctor

# Generate bash completion
ccx-agents completion bash > ~/.bash_completion.d/ccx-agents
```

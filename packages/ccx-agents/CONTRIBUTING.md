# Contributing to ccx-agents

Thanks for your interest! This document covers how to set up a development
environment, run tests, and submit changes.

## Development Setup

```bash
# Clone the repo
git clone https://github.com/BenedictKing/ccx.git
cd ccx/packages/ccx-agents

# Create and activate a virtual environment
python -m venv .venv
source .venv/bin/activate  # or: .venv\Scripts\activate on Windows

# Install with dev dependencies
pip install -e ".[dev]"

# (optional) Install type-checker for CI
pip install -e ".[ci]"
```

## Running Tests

```bash
# Unit tests (fast)
make test

# With coverage
make cover

# All tests including mock e2e
pytest tests/ -v

# E2E tests against real CCX Docker container
pytest tests/test_e2e.py --run-e2e -v
```

## Code Quality

```bash
# Lint
make lint

# Type check
make typecheck

# Format
ruff format src/ tests/
```

## Project Structure

```
src/ccx_agents/
├── __init__.py     # Public API exports
├── _client.py      # OpenAI client factory + CcxConfig
├── _setup.py       # ccx_setup() one-liner
├── config.py       # CcxConfigModel + CcxConfigProtocol
├── conv.py         # Multi-turn conversation
├── models.py       # Model → API-type mapping
├── router.py       # Agent-name routing + model-name routing
├── tracing.py      # Tracing integration
└── utils.py        # Helpers
```

## Making a Release

1. Update version in `src/ccx_agents/__init__.py`
2. Update `CHANGELOG.md`
3. Tag and push:

```bash
git tag v0.1.1
git push origin v0.1.1
```

CI will build and publish to PyPI automatically.

## Code of Conduct

Be respectful, constructive, and inclusive. We're all here to make AI
multi-model routing better.

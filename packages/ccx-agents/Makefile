.PHONY: test cover lint typecheck build clean install publish docs serve cli init doctor

install:
	pip install -e ".[dev,docs]"

test:
	python -m pytest tests/ -v

cover:
	python -m pytest tests/ --cov=src/ccx_agents --cov-report=term-missing --cov-report=html

lint:
	ruff check src/ tests/

typecheck:
	pyright src/ccx_agents/

docs:
	mkdocs build

serve:
	mkdocs serve

build:
	python -m build

clean:
	rm -rf dist/ build/ *.egg-info/ .pytest_cache/ __pycache__/ htmlcov/ site/
	find . -type d -name "__pycache__" -exec rm -rf {} + 2>/dev/null || true

publish:
	python -m twine upload dist/*

# CLI shortcuts
cli:
	python -m ccx_agents.cli

init:
	python -m ccx_agents.cli init

doctor:
	python -m ccx_agents.cli doctor

#!/usr/bin/env bash
set -euo pipefail

REPO_CLAUDE="anthropics/claude-code"
REPO_CODEX="openai/codex"

# Claude: protocol + new tools + usage changes
CLAUDE_KW="system|mid-conversation-system|thinking|tool_use|tool_result|stream|cache_control|stop_reason|refusal|compact|context_management|effort|adaptive|budget_tokens|max_tokens|output_config|code.execution|web.search|bash|text.editor|memory|files.api|tool.search|managed.agents|context.editing|compaction|prompt.caching|thinking.display|task.budget"

# Codex: protocol + new tools + usage changes (use word boundaries for generic terms)
CODEX_KW="function_call|function_call_output|compact|tool_choice|multi-agent|hosted.tools|web.search|image.generation|remote.control|code.mode|goal.extension|autonomous|plugin|skill|sandbox|permission|environment|session|reasoning|effort|rollout"

normalize_version() {
  echo "$1" | sed -E 's/^(rust-v|v|codex-cli |codex-cli-)//; s/ *\(Claude Code\)//'
}

match_keywords() {
  local text="$1"
  local pattern="$2"
  echo "$text" | grep -ioE "$pattern" | sort -u || true
}

fetch_json() {
  local json
  if ! json=$(gh api "$1" 2>/dev/null); then
    echo "{}"
    return 1
  fi
  echo "$json"
}

main() {
  local checked_at
  checked_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  local local_claude local_codex
  local_claude=$(normalize_version "$(claude --version 2>/dev/null | head -1)" || echo "unknown")
  local_codex=$(normalize_version "$(codex --version 2>/dev/null | head -1 || codex version 2>/dev/null | head -1)" || echo "unknown")

  local claude_json codex_json claude_tag codex_tag claude_body codex_body
  claude_json=$(fetch_json "repos/$REPO_CLAUDE/releases/latest") || true
  codex_json=$(fetch_json "repos/$REPO_CODEX/releases/latest") || true

  claude_tag=$(echo "$claude_json" | jq -r '.tag_name // empty' 2>/dev/null || echo "")
  codex_tag=$(echo "$codex_json" | jq -r '.tag_name // empty' 2>/dev/null || echo "")

  claude_body=$(echo "$claude_json" | jq -r '.body // empty' 2>/dev/null || echo "")
  codex_body=$(echo "$codex_json" | jq -r '.body // empty' 2>/dev/null || echo "")

  local claude_version codex_version
  claude_version=$(normalize_version "${claude_tag:-unknown}")
  codex_version=$(normalize_version "${codex_tag:-unknown}")

  local claude_matches codex_matches
  claude_matches=$(match_keywords "$claude_body" "$CLAUDE_KW" || true)
  codex_matches=$(match_keywords "$codex_body" "$CODEX_KW" || true)

  local claude_has_changes codex_has_changes
  if [[ -n "$claude_matches" ]]; then
    claude_has_changes=true
  else
    claude_has_changes=false
  fi

  if [[ -n "$codex_matches" ]]; then
    codex_has_changes=true
  else
    codex_has_changes=false
  fi

  local claude_up_to_date codex_up_to_date
  if [[ "$local_claude" == "$claude_version" ]]; then
    claude_up_to_date=true
  else
    claude_up_to_date=false
  fi

  if [[ "$local_codex" == "$codex_version" ]]; then
    codex_up_to_date=true
  else
    codex_up_to_date=false
  fi

  local claude_snippet codex_snippet
  claude_snippet=$(echo "$claude_body" | head -c 800)
  codex_snippet=$(echo "$codex_body" | head -c 800)

  jq -n \
    --arg checked_at "$checked_at" \
    --arg claude_remote_tag "$claude_tag" \
    --arg claude_remote_version "$claude_version" \
    --arg claude_local_version "$local_claude" \
    --argjson claude_up_to_date "$claude_up_to_date" \
    --argjson claude_protocol_changes "$claude_has_changes" \
    --arg claude_matched_keywords "$claude_matches" \
    --arg claude_release_body_snippet "$claude_snippet" \
    --arg codex_remote_tag "$codex_tag" \
    --arg codex_remote_version "$codex_version" \
    --arg codex_local_version "$local_codex" \
    --argjson codex_up_to_date "$codex_up_to_date" \
    --argjson codex_protocol_changes "$codex_has_changes" \
    --arg codex_matched_keywords "$codex_matches" \
    --arg codex_release_body_snippet "$codex_snippet" \
    '{
      checked_at: $checked_at,
      claude_code: {
        remote_tag: $claude_remote_tag,
        remote_version: $claude_remote_version,
        local_version: $claude_local_version,
        up_to_date: $claude_up_to_date,
        protocol_changes: $claude_protocol_changes,
        matched_keywords: (if $claude_matched_keywords == "" then [] else ($claude_matched_keywords | split("\n") | map(select(. != ""))) end),
        release_body_snippet: $claude_release_body_snippet
      },
      codex: {
        remote_tag: $codex_remote_tag,
        remote_version: $codex_remote_version,
        local_version: $codex_local_version,
        up_to_date: $codex_up_to_date,
        protocol_changes: $codex_protocol_changes,
        matched_keywords: (if $codex_matched_keywords == "" then [] else ($codex_matched_keywords | split("\n") | map(select(. != ""))) end),
        release_body_snippet: $codex_release_body_snippet
      }
    }'
}

main "$@"

package configservice

import (
	"fmt"
	"regexp"
	"strings"
)

func findJSONCStringValue(content string, key string) (string, bool) {
	re := regexp.MustCompile(`(?m)^(\s*)` + `"` + regexp.QuoteMeta(key) + `"` + `\s*:\s*\"([^\"\\]*(?:\\.[^\"\\]*)*)\"`)
	m := re.FindStringSubmatch(content)
	if len(m) < 3 {
		return "", false
	}
	return m[2], true
}

func extractJSONObjectRange(content string, key string) (int, int, bool) {
	re := regexp.MustCompile(`(?m)^(\s*)` + `"` + regexp.QuoteMeta(key) + `"` + `\s*:\s*\{`)
	loc := re.FindStringIndex(content)
	if loc == nil {
		return 0, 0, false
	}
	start := loc[0]
	pos := strings.IndexByte(content[start:], '{')
	if pos < 0 {
		return 0, 0, false
	}
	pos += start
	depth := 0
	inString := false
	inLineComment := false
	inBlockComment := false
	escaped := false
	i := pos
	for i < len(content) {
		ch := content[i]
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
			}
			i++
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(content) && content[i+1] == '/' {
				inBlockComment = false
				i += 2
				continue
			}
			i++
			continue
		}
		if inString {
			if escaped {
				escaped = false
				i++
				continue
			}
			if ch == '\\' {
				escaped = true
				i++
				continue
			}
			if ch == '"' {
				inString = false
			}
			i++
			continue
		}
		if ch == '/' && i+1 < len(content) {
			next := content[i+1]
			if next == '/' {
				inLineComment = true
				i += 2
				continue
			}
			if next == '*' {
				inBlockComment = true
				i += 2
				continue
			}
		}
		if ch == '"' {
			inString = true
			i++
			continue
		}
		if ch == '{' {
			depth++
			i++
			continue
		}
		if ch == '}' {
			depth--
			if depth == 0 {
				return start, i + 1, true
			}
			i++
			continue
		}
		i++
	}
	return 0, 0, false
}

func extractJSONObjectString(content string, key string) (string, bool) {
	start, end, ok := extractJSONObjectRange(content, key)
	if !ok {
		return "", false
	}
	return strings.TrimRight(content[start:end], "\n"), true
}

func jsonObjectKeyIndent(content string, start int, end int, key string) string {
	keyOffset := strings.Index(content[start:end], fmt.Sprintf("%q", key))
	if keyOffset < 0 {
		return ""
	}
	keyPos := start + keyOffset
	lineStart := strings.LastIndex(content[:keyPos], "\n") + 1
	return content[lineStart:keyPos]
}

func formatJSONObjectKey(key string, objectJSON string, indent string) string {
	if strings.HasPrefix(strings.TrimSpace(objectJSON), fmt.Sprintf("%q", key)) {
		return objectJSON
	}
	return fmt.Sprintf("%s%q: %s", indent, key, objectJSON)
}

func ensureJSONObjectKey(content string, parentKey string, childKey string, childJSON string) string {
	parentStart, parentEnd, ok := extractJSONObjectRange(content, parentKey)
	if !ok {
		childBlock := formatJSONObjectKey(childKey, childJSON, "    ")
		block := fmt.Sprintf("  %q: {\n%s\n  }", parentKey, childBlock)
		if idx := strings.LastIndex(content, "}"); idx >= 0 {
			head := content[:idx]
			tail := content[idx:]
			if strings.TrimSpace(head) != "" && !strings.HasSuffix(strings.TrimRight(head, " \t\r\n"), ",") && !strings.HasSuffix(strings.TrimRight(head, " \t\r\n"), "{") {
				head = strings.TrimRight(head, " \t\r\n") + ","
			}
			if !strings.HasSuffix(head, "\n") {
				head += "\n"
			}
			return head + block + "\n" + tail
		}
		if strings.TrimSpace(content) == "" {
			return "{\n" + block + "\n}"
		}
		if !strings.HasSuffix(content, "\n") {
			return content + "\n" + block + "\n"
		}
		return content + block + "\n"
	}
	parentIndent := jsonObjectKeyIndent(content, parentStart, parentEnd, parentKey)
	childStart, childEnd, childOK := extractJSONObjectRange(content[parentStart:parentEnd], childKey)
	if childOK {
		absoluteStart := parentStart + childStart
		absoluteEnd := parentStart + childEnd
		childIndent := jsonObjectKeyIndent(content, absoluteStart, absoluteEnd, childKey)
		childBlock := formatJSONObjectKey(childKey, childJSON, childIndent)
		return content[:absoluteStart] + childBlock + content[absoluteEnd:]
	}
	parentInner := content[parentStart:parentEnd]
	childBlock := formatJSONObjectKey(childKey, childJSON, parentIndent+"  ")
	insertPos := strings.LastIndex(parentInner, "}")
	if insertPos < 0 {
		return content
	}
	absInsert := parentStart + insertPos
	head := strings.TrimRight(content[:absInsert], " \t\r\n")
	tail := content[absInsert:]
	if strings.TrimSpace(head) != "" && !strings.HasSuffix(head, ",") && !strings.HasSuffix(head, "{") {
		head += ","
	}
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	return head + childBlock + "\n" + parentIndent + tail
}

func patchOpenCodeProviderJSONC(content string, providerID string, providerJSON string) string {
	return ensureJSONObjectKey(content, "provider", providerID, providerJSON)
}

func removeJSONCObjectKey(content string, key string) string {
	re := regexp.MustCompile(`(?m)^(\s*)` + `"` + regexp.QuoteMeta(key) + `"` + `\s*:\s*\{`)
	loc := re.FindStringIndex(content)
	if loc == nil {
		return content
	}
	start, end, ok := extractJSONObjectRange(content, key)
	if !ok {
		return content
	}
	if end < len(content) && content[end] == ',' {
		end++
	}
	if start > 0 && content[start-1] == ',' {
		start--
	}
	if start > 0 && (content[start-1] == '\n' || content[start-1] == '\r') {
		start--
		if start > 0 && content[start-1] == '\r' {
			start--
		}
	}
	if end < len(content) && (content[end] == '\n' || content[end] == '\r') {
		end++
	}
	return content[:start] + content[end:]
}

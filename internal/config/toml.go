package config

import (
	"fmt"
	"strconv"
	"strings"
)

// tomlData maps "section.key" (or "key" at top level) to raw parsed values.
type tomlData map[string]any

func parseTOML(src string) (tomlData, error) {
	out := make(tomlData)
	section := ""
	lines := strings.Split(src, "\n")

	for i := 0; i < len(lines); i++ {
		line := stripTOMLComment(lines[i])
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("line %d: unclosed section header", i+1)
			}
			sec := strings.TrimSpace(line[1 : len(line)-1])
			if sec == "" {
				return nil, fmt.Errorf("line %d: empty section name", i+1)
			}
			section = sec
			continue
		}

		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected key = value", i+1)
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", i+1)
		}
		valRaw := strings.TrimSpace(line[eq+1:])

		// Support multiline arrays by joining lines until closing ']'.
		if strings.HasPrefix(valRaw, "[") && !hasClosedBracket(valRaw) {
			var b strings.Builder
			b.WriteString(valRaw)
			closed := false
			for i+1 < len(lines) {
				i++
				next := strings.TrimSpace(stripTOMLComment(lines[i]))
				if next == "" {
					continue
				}
				b.WriteByte(' ')
				b.WriteString(next)
				if hasClosedBracket(b.String()) {
					closed = true
					break
				}
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unclosed array", i+1)
			}
			valRaw = b.String()
		}

		val, err := parseTOMLValue(valRaw)
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %w", i+1, key, err)
		}
		fullKey := key
		if section != "" {
			fullKey = section + "." + key
		}
		out[fullKey] = val
	}
	return out, nil
}

func stripTOMLComment(line string) string {
	inDouble := false
	inSingle := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			escaped = false
			continue
		}
		if inDouble {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		switch c {
		case '"':
			inDouble = true
		case '\'':
			inSingle = true
		case '#':
			return line[:i]
		}
	}
	return line
}

func hasClosedBracket(s string) bool {
	inDouble := false
	inSingle := false
	escaped := false
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inDouble {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		switch c {
		case '"':
			inDouble = true
		case '\'':
			inSingle = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

func parseTOMLValue(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty value")
	}
	switch {
	case s == "true":
		return true, nil
	case s == "false":
		return false, nil
	case strings.HasPrefix(s, `"`):
		if !strings.HasSuffix(s, `"`) || len(s) < 2 {
			return nil, fmt.Errorf("unterminated string")
		}
		v, err := strconv.Unquote(s)
		if err != nil {
			return nil, fmt.Errorf("invalid string: %w", err)
		}
		return v, nil
	case strings.HasPrefix(s, `'`):
		if !strings.HasSuffix(s, `'`) || len(s) < 2 {
			return nil, fmt.Errorf("unterminated literal string")
		}
		return s[1 : len(s)-1], nil
	case strings.HasPrefix(s, `[`):
		return parseTOMLStringArray(s)
	default:
		if strings.ContainsAny(s, ".eE") {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid float %q", s)
			}
			return f, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q", s)
		}
		return n, nil
	}
}

func parseTOMLStringArray(s string) ([]string, error) {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("invalid array")
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return []string{}, nil
	}
	var items []string
	var cur strings.Builder
	inDouble := false
	inSingle := false
	escaped := false

	flush := func() error {
		tok := strings.TrimSpace(cur.String())
		cur.Reset()
		if tok == "" {
			return nil
		}
		val, err := parseTOMLValue(tok)
		if err != nil {
			return err
		}
		str, ok := val.(string)
		if !ok {
			return fmt.Errorf("array elements must be strings")
		}
		items = append(items, str)
		return nil
	}

	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if escaped {
			escaped = false
			cur.WriteByte(c)
			continue
		}
		if inDouble {
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inDouble = false
			}
			cur.WriteByte(c)
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '"':
			inDouble = true
			cur.WriteByte(c)
		case '\'':
			inSingle = true
			cur.WriteByte(c)
		case ',':
			if err := flush(); err != nil {
				return nil, err
			}
		default:
			cur.WriteByte(c)
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return items, nil
}

func (d tomlData) getString(key string) (string, bool, error) {
	v, ok := d[key]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("config %s must be a string", key)
	}
	return s, true, nil
}

func (d tomlData) getBool(key string) (bool, bool, error) {
	v, ok := d[key]
	if !ok {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, false, fmt.Errorf("config %s must be a boolean", key)
	}
	return b, true, nil
}

func (d tomlData) getFloat(key string) (float64, bool, error) {
	v, ok := d[key]
	if !ok {
		return 0, false, nil
	}
	switch n := v.(type) {
	case float64:
		return n, true, nil
	case int64:
		return float64(n), true, nil
	default:
		return 0, false, fmt.Errorf("config %s must be a number", key)
	}
}

func (d tomlData) getInt(key string) (int, bool, error) {
	v, ok := d[key]
	if !ok {
		return 0, false, nil
	}
	n, ok := v.(int64)
	if !ok {
		return 0, false, fmt.Errorf("config %s must be an integer", key)
	}
	return int(n), true, nil
}

func (d tomlData) getStringSlice(key string) ([]string, bool, error) {
	v, ok := d[key]
	if !ok {
		return nil, false, nil
	}
	sl, ok := v.([]string)
	if !ok {
		return nil, false, fmt.Errorf("config %s must be an array of strings", key)
	}
	return sl, true, nil
}

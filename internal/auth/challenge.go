package auth

import "strings"

// bearerChallenge finds the first Bearer challenge among the WWW-Authenticate
// header values and returns its parameters, keyed by lower-case name.
func bearerChallenge(headers []string) (map[string]string, bool) {
	for _, h := range headers {
		scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			continue
		}
		return parseParams(rest), true
	}
	return nil, false
}

// parseParams parses a comma-separated list of key=value pairs, where values may
// be quoted and may contain commas. Quoted values do not support escapes; the
// token services in use do not produce them.
func parseParams(s string) map[string]string {
	params := make(map[string]string)
	for {
		s = strings.TrimLeft(s, " ,")
		if s == "" {
			return params
		}
		key, rest, ok := strings.Cut(s, "=")
		if !ok {
			return params
		}
		key = strings.ToLower(strings.TrimSpace(key))

		var value string
		if quoted, ok := strings.CutPrefix(rest, `"`); ok {
			end := strings.IndexByte(quoted, '"')
			if end < 0 {
				value, rest = quoted, ""
			} else {
				value, rest = quoted[:end], quoted[end+1:]
			}
		} else {
			value, rest, _ = strings.Cut(rest, ",")
			value = strings.TrimSpace(value)
		}
		params[key] = value
		s = rest
	}
}

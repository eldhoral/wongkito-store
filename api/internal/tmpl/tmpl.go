package tmpl

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrMissingKey is returned when the template names a key the order does
	// not carry.
	ErrMissingKey = errors.New("template: missing key")
	// ErrEmpty is returned when rendering produces an empty customer number.
	ErrEmpty = errors.New("template: empty result")
	// ErrUnmatchedBrace is returned when the result still contains { or }.
	ErrUnmatchedBrace = errors.New("template: unmatched brace")
)

var (
	placeholderRE = regexp.MustCompile(`\{([^{}]*)\}`)
	validRE       = regexp.MustCompile(`^([a-zA-Z0-9_ ]+)(?:\|(digits|msisdn))?$`)
)

// Render replaces {key}, {key|digits} and {key|msisdn} placeholders in
// template with trimmed values from info.
func Render(template string, info map[string]string) (string, error) {
	var out strings.Builder
	last := 0
	for _, match := range placeholderRE.FindAllStringSubmatchIndex(template, -1) {
		out.WriteString(template[last:match[0]])
		inner := template[match[2]:match[3]]
		parts := validRE.FindStringSubmatch(inner)
		if parts == nil {
			return "", fmt.Errorf("template: invalid placeholder %q", inner)
		}
		key, filter := parts[1], parts[2]
		value, ok := info[key]
		if !ok {
			return "", fmt.Errorf("%w: %s", ErrMissingKey, key)
		}
		value = strings.TrimSpace(value)
		switch filter {
		case "digits":
			value = onlyDigits(value)
		case "msisdn":
			value = onlyDigits(value)
			if strings.HasPrefix(value, "62") {
				value = "0" + value[2:]
			}
		}
		if value == "" {
			return "", fmt.Errorf("%w: %s", ErrEmpty, key)
		}
		out.WriteString(value)
		last = match[1]
	}
	out.WriteString(template[last:])
	result := out.String()
	if strings.TrimSpace(result) == "" {
		return "", ErrEmpty
	}
	// A typo like "{player_id}|{zone_id" must not send a half-filled number.
	if strings.ContainsAny(result, "{}") {
		return "", fmt.Errorf("%w: %q", ErrUnmatchedBrace, result)
	}
	return result, nil
}

// Keys returns the placeholder names in template, in order and without
// duplicates. Filters are stripped.
func Keys(template string) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, match := range placeholderRE.FindAllStringSubmatch(template, -1) {
		parts := validRE.FindStringSubmatch(match[1])
		if parts == nil {
			continue
		}
		key := parts[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

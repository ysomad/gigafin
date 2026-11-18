// Package redact hides sensitive values, such as phone numbers, for display.
package redact

import "strings"

// Tail replaces every character of s but the last n with •, so +79991234567 with n 4
// is ••••••••4567.
func Tail(s string, n int) string {
	r := []rune(s)
	hidden := max(len(r)-n, 0)
	return strings.Repeat("•", hidden) + string(r[hidden:])
}

package redact_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ysomad/gigafin/internal/redact"
)

func TestTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"+79991234567", 4, "••••••••4567"},
		{"1234", 4, "1234"},
		{"12", 4, "12"},
		{"", 4, ""},
		{"почта", 2, "•••та"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, redact.Tail(tt.s, tt.n), tt.s)
	}
}

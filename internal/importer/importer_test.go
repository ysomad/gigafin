package importer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
)

func TestParseUnknownSource(t *testing.T) {
	t.Parallel()

	_, err := importer.Parse(domain.ImportSource("sber"), strings.NewReader(""), time.UTC)
	require.ErrorIs(t, err, errors.ErrInvalid)
	require.ErrorContains(t, err, `unknown "sber"`)
}

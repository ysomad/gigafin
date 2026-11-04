package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/repository/repotest"
)

func TestSettings(t *testing.T) {
	t.Parallel()

	svc := newServices(t, repotest.New(t))
	ctx := t.Context()
	u, err := svc.Auth.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)

	s, err := svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.Settings{}, s, "defaults before the first change, rates off")

	require.NoError(t, svc.Settings.SetDisplay(ctx, u.ID, true))
	s, err = svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, s.ShowMinorUnits)

	require.NoError(t, svc.Settings.SetRates(ctx, u.ID, domain.RateSourceCBR, "USD"))
	s, err = svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.Settings{ShowMinorUnits: true, RateSource: domain.RateSourceCBR, PrimaryCurrency: "USD"}, s,
		"rates keep the display settings")

	require.NoError(t, svc.Settings.SetDisplay(ctx, u.ID, false))
	s, err = svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.Settings{RateSource: domain.RateSourceCBR, PrimaryCurrency: "USD"}, s,
		"display keeps the rate settings")

	err = svc.Settings.SetRates(ctx, u.ID, domain.RateSourceCBR, "")
	require.ErrorIs(t, err, errors.ErrInvalid, "rates need a primary currency")
	err = svc.Settings.SetRates(ctx, u.ID, domain.RateSourceCBR, "XXX")
	require.ErrorIs(t, err, errors.ErrInvalid)
	err = svc.Settings.SetRates(ctx, u.ID, "ecb", "USD")
	require.ErrorIs(t, err, errors.ErrInvalid)

	require.NoError(t, svc.Settings.SetRates(ctx, u.ID, domain.RateSourceNone, "USD"))
	s, err = svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RateSourceNone, s.RateSource)
}

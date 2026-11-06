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
	assert.Equal(t, domain.DefaultSettings(), s, "defaults before the first change, rates off")
	assert.True(t, s.AutoCategoryRules, "rules are learned by default")

	want := domain.Settings{
		ShowMinorUnits: true, RateSource: domain.RateSourceCBR, PrimaryCurrency: "USD", Language: "ru",
	}
	require.NoError(t, svc.Settings.Set(ctx, u.ID, want))
	s, err = svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, want, s, "stored as a whole")

	want = domain.Settings{AutoCategoryRules: true}
	require.NoError(t, svc.Settings.Set(ctx, u.ID, want))
	s, err = svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, want, s, "fields left out are reset")

	err = svc.Settings.Set(ctx, u.ID, domain.Settings{RateSource: domain.RateSourceCBR})
	require.ErrorIs(t, err, errors.ErrInvalid, "rates need a primary currency")
	err = svc.Settings.Set(ctx, u.ID, domain.Settings{RateSource: domain.RateSourceCBR, PrimaryCurrency: "XXX"})
	require.ErrorIs(t, err, errors.ErrInvalid)
	err = svc.Settings.Set(ctx, u.ID, domain.Settings{RateSource: "ecb", PrimaryCurrency: "USD"})
	require.ErrorIs(t, err, errors.ErrInvalid)
	err = svc.Settings.Set(ctx, u.ID, domain.Settings{Language: "not a tag!"})
	require.ErrorIs(t, err, errors.ErrInvalid)

	s, err = svc.Settings.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, want, s, "invalid settings are not stored")
}

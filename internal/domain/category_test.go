package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
)

func TestCategoryCheckDelete(t *testing.T) {
	t.Parallel()

	require.NoError(t, domain.Category{}.CheckDelete(0, 0, 0))
	require.ErrorIs(t, domain.Category{}.CheckDelete(1, 0, 0), errors.ErrConflict)
	require.ErrorIs(t, domain.Category{}.CheckDelete(0, 1, 0), errors.ErrConflict)
	require.ErrorIs(t, domain.Category{}.CheckDelete(0, 0, 1), errors.ErrConflict)
}

func TestParentChoices(t *testing.T) {
	t.Parallel()

	tree := domain.NewTree([]domain.Category{
		{ID: 1, Kind: domain.KindExpense, Name: "Food"},
		{ID: 2, Kind: domain.KindExpense, Name: "Home"},
		{ID: 3, Kind: domain.KindIncome, Name: "Salary"},
		{ID: 4, ParentID: 1, Kind: domain.KindExpense, Name: "Cafe"},
	})

	parents, name := domain.ParentChoices(tree, domain.Category{ID: 4, ParentID: 1, Kind: domain.KindExpense})
	assert.Equal(t, "Food", name)
	require.Len(t, parents, 2)
	assert.Equal(t, int64(1), parents[0].ID)
	assert.Nil(t, parents[0].Children)
	assert.Equal(t, int64(2), parents[1].ID)

	parents, name = domain.ParentChoices(tree, domain.Category{ID: 1, Kind: domain.KindExpense})
	assert.Nil(t, parents)
	assert.Empty(t, name)
}

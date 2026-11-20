package categories

import (
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// listData is the categories page: the categories of Groups, filtered by Kind, all
// when empty, Query and Status.
type listData struct {
	Kind   domain.Kind
	Query  string
	Status string // "" all, active or archived
	Groups []kindGroup
}

// kindGroup are the categories of a kind on the categories page.
type kindGroup struct {
	Kind       domain.Kind
	Categories []domain.Category
}

// categoriesData is the new category page of Kind with the values of its form.
type categoriesData struct {
	Categories []domain.Category
	Kind       domain.Kind `form:"kind"`
	Parent     int64       `form:"parent"` // 0 is a cat1
	Name       string      `form:"name"`
}

// categoryData is the category page with the values of its edit form.
type categoryData struct {
	Category   domain.Category
	ParentName string
	Parents    []domain.Category
	Name       string `form:"name"`
	Parent     int64  `form:"parent"` // 0 keeps the parent
	// Days are the first page of the category's operations, MoreURL the next one.
	Days     []kit.DayGroup
	CatNames map[int64]string
	AccByID  map[int64]domain.Account
	MoreURL  string
}

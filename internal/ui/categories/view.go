package categories

import "github.com/ysomad/gigafin/internal/domain"

// categoriesData is the categories page of Kind, or the new category page with the
// values of its form.
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
}

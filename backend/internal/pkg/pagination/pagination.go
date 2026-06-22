// Package pagination provides cursor/offset pagination helpers.
package pagination

import "math"

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Page holds parsed pagination parameters.
type Page struct {
	Limit  int32
	Offset int32
}

// New normalizes limit and page number into a Page.
// page is 1-indexed.
func New(limit, page int) Page {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	if page <= 0 {
		page = 1
	}
	return Page{
		Limit:  int32(limit),
		Offset: int32((page - 1) * limit),
	}
}

// Meta holds pagination metadata for API responses.
type Meta struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

// NewMeta builds response metadata.
func NewMeta(page, perPage, totalItems int) Meta {
	totalPages := 0
	if perPage > 0 {
		totalPages = int(math.Ceil(float64(totalItems) / float64(perPage)))
	}
	return Meta{
		Page:       page,
		PerPage:    perPage,
		TotalItems: totalItems,
		TotalPages: totalPages,
	}
}

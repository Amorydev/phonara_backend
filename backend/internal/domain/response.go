// Package domain defines shared request/response DTOs and domain types.
package domain

// Response is the standard JSON envelope for all API responses.
type Response struct {
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

// PagedResponse wraps paginated results.
type PagedResponse struct {
	Data any      `json:"data"`
	Meta PageMeta `json:"meta"`
}

// PageMeta is pagination metadata.
type PageMeta struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

// OK builds a success response envelope.
func OK(data any) Response {
	return Response{Data: data}
}

// Err builds an error response envelope.
func Err(msg string) Response {
	return Response{Error: msg}
}

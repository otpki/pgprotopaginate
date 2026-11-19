package paginate

import (
	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
)

type FilterGroup struct {
	Operator queryPB.LogicalOperator `json:"operator"`
	Filters  []*queryPB.Filter       `json:"filters"`
}

type SortEntry struct {
	Column        string            `json:"column"`
	Order         queryPB.SortOrder `json:"order"`
	JSONPath      string            `json:"json_path"`
	JSONText      string            `json:"json_text"`
	JSONNumeric   string            `json:"json_numeric"`
	ArrayIndex    int64             `json:"array_index"`
	ArrayLength   bool              `json:"array_length"`
	ArrayContains any               `json:"array_contains"`
}

type PaginationParams struct {
	// Pagination
	PageToken string `json:"page_token"`
	PageSize  int64  `json:"page_size"`
	Skip      int64  `json:"skip"`

	// Sorting
	Sort []*SortEntry `json:"sort"`

	// Filtering
	Eq         map[string][]any    `json:"eq"`
	NotEq      map[string][]any    `json:"not_eq"`
	Gt         map[string]any      `json:"gt"`
	Gte        map[string]any      `json:"gte"`
	Lt         map[string]any      `json:"lt"`
	Lte        map[string]any      `json:"lte"`
	Between    map[string][2]any   `json:"between"`
	In         map[string][]any    `json:"in"`
	NotIn      map[string][]any    `json:"not_in"`
	Like       map[string][]string `json:"like"`
	ILike      map[string][]string `json:"ilike"`
	IsNull     []string            `json:"is_null"`
	IsNotNull  []string            `json:"is_not_null"`
	IsEmpty    []string            `json:"is_empty"`
	IsNotEmpty []string            `json:"is_not_empty"`

	// JSON Filtering
	JSONContains  map[string][]string `json:"json_contains"`
	JSONExists    map[string][]string `json:"json_exists"`
	JSONAnyExists map[string][]string `json:"json_any_exists"`
	JSONAllExists map[string][]string `json:"json_all_exists"`
	JSONPathQuery map[string][]string `json:"json_path_query"`

	// Array Filtering
	ArrayContains    map[string][]any `json:"array_contains"`
	ArrayContainedBy map[string][]any `json:"array_contained_by"`
	ArrayOverlap     map[string][]any `json:"array_overlap"`

	// Filter Groups (for OR logic)
	FilterGroups []*FilterGroup `json:"filter_groups"`

	// Extra Settings
	table                   string
	schema                  string
	model                   any
	additionalColumnTypes   map[string]any
	selectColumns           []string
	additionalSelectColumns []string
	whereClauses            []string
	whereArgs               []any
	joins                   []string
	groupBy                 string
	WithTokenPagination     bool     `json:"with_token_pagination"`
	WithoutOffset           bool     `json:"without_offset"`
	WithCountEstimate       bool     `json:"with_count_estimate"`
	WithoutCount            bool     `json:"without_count"`
	WithPaginationInfoOnly  bool     `json:"with_pagination_info_only"`
	CursorColumns           []string `json:"cursor_columns"`
}

type PageInfo struct {
	TotalCount int64 `json:"total_count"`
	HasMore    bool  `json:"has_more"`
}

type PaginationResponse struct {
	PageInfo PageInfo       `json:"page_info"`
	Data     []any          `json:"data"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func NewPaginationResponse() *PaginationResponse {
	return &PaginationResponse{
		PageInfo: PageInfo{},
		Data:     []any{},
	}
}

// NewPaginationParams initializes a PaginationParams with all maps allocated
func NewPaginationParams() *PaginationParams {
	return &PaginationParams{
		Eq:               make(map[string][]any),
		NotEq:            make(map[string][]any),
		Gt:               make(map[string]any),
		Gte:              make(map[string]any),
		Lt:               make(map[string]any),
		Lte:              make(map[string]any),
		Between:          make(map[string][2]any),
		In:               make(map[string][]any),
		NotIn:            make(map[string][]any),
		Like:             make(map[string][]string),
		ILike:            make(map[string][]string),
		IsNull:           []string{},
		IsNotNull:        []string{},
		IsEmpty:          []string{},
		IsNotEmpty:       []string{},
		JSONContains:     make(map[string][]string),
		JSONExists:       make(map[string][]string),
		JSONAnyExists:    make(map[string][]string),
		JSONAllExists:    make(map[string][]string),
		JSONPathQuery:    make(map[string][]string),
		ArrayContains:    make(map[string][]any),
		ArrayContainedBy: make(map[string][]any),
		ArrayOverlap:     make(map[string][]any),
		FilterGroups:     []*FilterGroup{},
	}
}

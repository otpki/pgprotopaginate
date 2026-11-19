package paginate

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
)

func BindQueryParamsToStruct(queryParams url.Values, params *PaginationParams) error {
	// Initialize all maps using the helper
	*params = *NewPaginationParams()

	// bind simple fields
	if values := queryParams["page_token"]; len(values) > 0 {
		params.PageToken = values[0]
	}
	if values := queryParams["page_size"]; len(values) > 0 {
		if size, err := strconv.ParseInt(values[0], 10, 64); err == nil {
			params.PageSize = size
		}
	}
	if values := queryParams["skip"]; len(values) > 0 {
		if skip, err := strconv.ParseInt(values[0], 10, 64); err == nil {
			params.Skip = skip
		}
	}
	if values := queryParams["with_token_pagination"]; len(values) > 0 {
		params.WithTokenPagination = values[0] == "true"
	}
	// if values := queryParams["without_offset"]; len(values) > 0 {
	// 	params.WithoutOffset = values[0] == "true"
	// }
	if values := queryParams["with_count_estimate"]; len(values) > 0 {
		params.WithCountEstimate = values[0] == "true"
	}

	if values := queryParams["without_count"]; len(values) > 0 {
		params.WithoutCount = values[0] == "true"
	}

	if values := queryParams["with_pagination_info_only"]; len(values) > 0 {
		params.WithPaginationInfoOnly = values[0] == "true"
	}

	// bind sort parameters
	if err := bindSortParams(queryParams, params); err != nil {
		return err
	}

	// bind filter parameters
	if err := bindFilterParams(queryParams, params); err != nil {
		return err
	}

	// bind filter groups
	if err := bindFilterGroups(queryParams, params); err != nil {
		return err
	}

	return nil
}

func BindQueryStringToStruct(query string) (*PaginationParams, error) {
	queryParams, err := url.ParseQuery(query)
	if err != nil {
		return nil, err
	}
	params := NewPaginationParams()
	if err := BindQueryParamsToStruct(queryParams, params); err != nil {
		return nil, err
	}
	return params, nil
}

func bindSortParams(queryParams url.Values, params *PaginationParams) error {
	// handle simple sort: sort=column (ASC) or sort=-column (DESC)
	if values := queryParams["sort"]; len(values) > 0 {
		for _, sortStr := range values {
			if after, ok := strings.CutPrefix(sortStr, "-"); ok {
				entry := &SortEntry{
					Column: after,
					Order:  queryPB.SortOrder_SORT_ORDER_DESC,
				}
				params.Sort = append(params.Sort, entry)
			} else {
				entry := &SortEntry{
					Column: sortStr,
					Order:  queryPB.SortOrder_SORT_ORDER_ASC,
				}
				params.Sort = append(params.Sort, entry)
			}
		}
	}

	// handle advanced sort parameters
	for key, values := range queryParams {
		if strings.HasPrefix(key, "sort[") && strings.HasSuffix(key, "]") {
			fieldName := key[5 : len(key)-1] // extract field name between brackets
			if len(values) > 0 {
				entry := getOrCreateSortEntry(params, fieldName)
				setSortField(entry, fieldName, values[0])
			}
		}
	}

	return nil
}

func bindFilterParams(queryParams url.Values, params *PaginationParams) error {
	for key, values := range queryParams {
		if len(values) == 0 {
			continue
		}

		switch {
		case strings.HasPrefix(key, "eq["):
			bindMapParam(params.Eq, key, values)
		case strings.HasPrefix(key, "not_eq["):
			bindMapParam(params.NotEq, key, values)
		case strings.HasPrefix(key, "gt["):
			bindSingleMapParam(params.Gt, key, values)
		case strings.HasPrefix(key, "gte["):
			bindSingleMapParam(params.Gte, key, values)
		case strings.HasPrefix(key, "lt["):
			bindSingleMapParam(params.Lt, key, values)
		case strings.HasPrefix(key, "lte["):
			bindSingleMapParam(params.Lte, key, values)
		case strings.HasPrefix(key, "between["):
			bindBetweenParam(params.Between, key, values)
		case strings.HasPrefix(key, "in["):
			bindMapParam(params.In, key, values)
		case strings.HasPrefix(key, "not_in["):
			bindMapParam(params.NotIn, key, values)
		case strings.HasPrefix(key, "like["):
			bindStringMapParam(params.Like, key, values)
		case strings.HasPrefix(key, "ilike["):
			bindStringMapParam(params.ILike, key, values)
		case key == "is_null":
			params.IsNull = append(params.IsNull, values...)
		case key == "is_not_null":
			params.IsNotNull = append(params.IsNotNull, values...)
		case key == "is_empty":
			params.IsEmpty = append(params.IsEmpty, values...)
		case key == "is_not_empty":
			params.IsNotEmpty = append(params.IsNotEmpty, values...)
		case strings.HasPrefix(key, "json_contains["):
			bindStringMapParam(params.JSONContains, key, values)
		case strings.HasPrefix(key, "json_exists["):
			bindStringMapParam(params.JSONExists, key, values)
		case strings.HasPrefix(key, "json_any_exists["):
			bindStringMapParam(params.JSONAnyExists, key, values)
		case strings.HasPrefix(key, "json_all_exists["):
			bindStringMapParam(params.JSONAllExists, key, values)
		case strings.HasPrefix(key, "json_path_query["):
			bindStringMapParam(params.JSONPathQuery, key, values)
		case strings.HasPrefix(key, "array_contains["):
			bindMapParam(params.ArrayContains, key, values)
		case strings.HasPrefix(key, "array_contained_by["):
			bindMapParam(params.ArrayContainedBy, key, values)
		case strings.HasPrefix(key, "array_overlap["):
			bindMapParam(params.ArrayOverlap, key, values)
		}
	}

	return nil
}

func getOrCreateSortEntry(params *PaginationParams, fieldName string) *SortEntry {
	// find an existing entry for this column
	for _, entry := range params.Sort {
		if entry.Column == fieldName {
			return entry
		}
	}
	// create a new entry
	entry := &SortEntry{
		Column: fieldName,
		Order:  queryPB.SortOrder_SORT_ORDER_ASC,
	}
	params.Sort = append(params.Sort, entry)
	return entry
}

func setSortField(entry *SortEntry, field, value string) {
	switch field {
	case "json_path":
		entry.JSONPath = value
	case "json_text":
		entry.JSONText = value
	case "json_numeric":
		entry.JSONNumeric = value
	case "array_index":
		if index, err := strconv.ParseInt(value, 10, 64); err == nil {
			entry.ArrayIndex = index
		}
	case "array_length":
		entry.ArrayLength = value == "true"
	case "array_contains":
		entry.ArrayContains = value
	}
}

func bindMapParam(target map[string][]any, key string, values []string) {
	fieldName := extractFieldName(key)
	if fieldName == "" {
		return
	}
	anyValues := make([]any, len(values))
	for i, v := range values {
		anyValues[i] = v
	}
	target[fieldName] = anyValues
}

func bindSingleMapParam(target map[string]any, key string, values []string) {
	fieldName := extractFieldName(key)
	if fieldName == "" || len(values) == 0 {
		return
	}
	target[fieldName] = values[0]
}

func bindStringMapParam(target map[string][]string, key string, values []string) {
	fieldName := extractFieldName(key)
	if fieldName == "" {
		return
	}
	target[fieldName] = values
}

func bindBetweenParam(target map[string][2]any, key string, values []string) {
	fieldName := extractFieldName(key)
	if fieldName == "" || len(values) == 0 {
		return
	}
	if len(values) >= 2 {
		target[fieldName] = [2]any{values[0], values[1]}
		return
	}

	parts := strings.SplitN(values[0], ",", 2)
	if len(parts) < 2 {
		return
	}
	target[fieldName] = [2]any{parts[0], parts[1]}
}

func extractFieldName(key string) string {
	start := strings.Index(key, "[")
	end := strings.Index(key, "]")
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return key[start+1 : end]
}

func bindFilterGroups(queryParams url.Values, params *PaginationParams) error {
	// Filter groups are expected in format:
	// filter_groups[0][operator]=OR
	// filter_groups[0][filters][0][column]=name
	// filter_groups[0][filters][0][operator]=ILIKE
	// filter_groups[0][filters][0][values][0]=test

	groupsMap := make(map[int]*FilterGroup)

	for key, values := range queryParams {
		if !strings.HasPrefix(key, "filter_groups[") {
			continue
		}

		// Parse filter_groups[index][field] or filter_groups[index][filters][filter_index][field]
		parts := strings.Split(key, "[")
		if len(parts) < 2 {
			continue
		}

		// Extract group index
		groupIndexStr := strings.TrimSuffix(parts[1], "]")
		groupIndex, err := strconv.Atoi(groupIndexStr)
		if err != nil || groupIndex < 0 || groupIndex >= len(queryParams) {
			return fmt.Errorf("invalid filter group index %q", groupIndexStr)
		}

		// Create or get the group
		if groupsMap[groupIndex] == nil {
			groupsMap[groupIndex] = &FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR, // default
				Filters:  []*queryPB.Filter{},
			}
		}
		group := groupsMap[groupIndex]

		// Check if this is a group-level field or a filter field
		if len(parts) == 3 {
			// Group-level field: operator
			field := strings.TrimSuffix(parts[2], "]")
			if field == "operator" && len(values) > 0 {
				switch values[0] {
				case "OR":
					group.Operator = queryPB.LogicalOperator_LOGICAL_OPERATOR_OR
				case "AND":
					group.Operator = queryPB.LogicalOperator_LOGICAL_OPERATOR_AND
				}
			}
		} else if len(parts) >= 5 && parts[2] == "filters]" {
			// Filter field: filters[index][field]
			filterIndexStr := strings.TrimSuffix(parts[3], "]")
			filterIndex, err := strconv.Atoi(filterIndexStr)
			if err != nil || filterIndex < 0 || filterIndex >= len(queryParams) {
				return fmt.Errorf("invalid group filter index %q", filterIndexStr)
			}

			// Ensure filters array is large enough
			for len(group.Filters) <= filterIndex {
				group.Filters = append(group.Filters, &queryPB.Filter{})
			}
			filter := group.Filters[filterIndex]

			field := strings.TrimSuffix(parts[4], "]")
			switch field {
			case "column":
				if len(values) > 0 {
					filter.Column = values[0]
				}
			case "operator":
				if len(values) > 0 {
					// Convert string operator to enum
					switch values[0] {
					case "EQUALS":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_EQUALS
					case "NOT_EQUALS":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_NOT_EQUALS
					case "GREATER_THAN":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN
					case "GREATER_THAN_OR_EQUAL":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN_OR_EQUAL
					case "LESS_THAN":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN
					case "LESS_THAN_OR_EQUAL":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN_OR_EQUAL
					case "LIKE":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_LIKE
					case "ILIKE":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_ILIKE
					case "IN":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_IN
					case "NOT_IN":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_NOT_IN
					case "IS_NULL":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL
					case "IS_NOT_NULL":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_NULL
					case "IS_EMPTY":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_IS_EMPTY
					case "IS_NOT_EMPTY":
						filter.Operator = queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_EMPTY
					}
				}
			case "values":
				// values are indexed: filter_groups[0][filters][0][values][0]
				if len(parts) >= 6 {
					valueIndexStr := strings.TrimSuffix(parts[5], "]")
					valueIndex, err := strconv.Atoi(valueIndexStr)
					if err != nil || valueIndex < 0 || valueIndex >= len(queryParams) {
						return fmt.Errorf("invalid filter value index %q", valueIndexStr)
					}
					// Ensure values array is large enough
					for len(filter.Values) <= valueIndex {
						filter.Values = append(filter.Values, &queryPB.FilterValue{})
					}
					if len(values) > 0 {
						filter.Values[valueIndex].StringValue = values[0]
					}
				}
			}
		}
	}

	// Convert map to sorted slice
	maxIndex := 0
	for idx := range groupsMap {
		if idx > maxIndex {
			maxIndex = idx
		}
	}
	params.FilterGroups = make([]*FilterGroup, maxIndex+1)
	for idx, group := range groupsMap {
		params.FilterGroups[idx] = group
	}

	return nil
}

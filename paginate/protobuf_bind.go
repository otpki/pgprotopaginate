package paginate

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"time"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConvertToProtoRequest converts PaginationParams to PaginationRequest
func ConvertToProtoRequest(params *PaginationParams) *queryPB.PaginationRequest {
	return ConvertToProtoRequestWithDescriptor(params, nil)
}

// ConvertToProtoRequestWithDescriptor converts PaginationParams to PaginationRequest with type-aware conversion
func ConvertToProtoRequestWithDescriptor(params *PaginationParams, fd protoreflect.MessageDescriptor) *queryPB.PaginationRequest {
	req := &queryPB.PaginationRequest{
		PageToken:              params.PageToken,
		PageSize:               params.PageSize,
		Skip:                   params.Skip,
		WithCountEstimate:      params.WithCountEstimate,
		WithoutCount:           params.WithoutCount,
		WithPaginationInfoOnly: params.WithPaginationInfoOnly,
		WithTokenPagination:    params.WithTokenPagination,
	}

	// convert sort entries
	for _, sortEntry := range params.Sort {
		pbSortEntry := &queryPB.SortEntry{
			Column: sortEntry.Column,
			Order:  sortEntry.Order,
		}
		if sortEntry.JSONPath != "" {
			pbSortEntry.JsonPath = sortEntry.JSONPath
		}
		if sortEntry.JSONText != "" {
			pbSortEntry.JsonText = sortEntry.JSONText
		}
		if sortEntry.JSONNumeric != "" {
			pbSortEntry.JsonNumeric = sortEntry.JSONNumeric
		}
		if sortEntry.ArrayIndex != 0 {
			pbSortEntry.ArrayIndex = sortEntry.ArrayIndex
		}
		if sortEntry.ArrayLength {
			pbSortEntry.ArrayLength = sortEntry.ArrayLength
		}
		if sortEntry.ArrayContains != nil {
			// convert any to string for protobuf
			if str, ok := sortEntry.ArrayContains.(string); ok {
				pbSortEntry.ArrayContains = str
			}
		}
		req.Sort = append(req.Sort, pbSortEntry)
	}

	// convert filters
	req.Filters = convertFiltersToProtobuf(params, fd)

	// convert filter groups
	req.FilterGroups = convertFilterGroupsToProtobuf(params)

	return req
}

func convertFiltersToProtobuf(params *PaginationParams, fd protoreflect.MessageDescriptor) []*queryPB.Filter {
	var filters []*queryPB.Filter

	for column, values := range params.Eq {
		operator := queryPB.FilterOperator_FILTER_OPERATOR_EQUALS
		if len(values) > 1 {
			operator = queryPB.FilterOperator_FILTER_OPERATOR_IN
		}
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: operator,
			Values:   convertValuesToProtobufWithType(values, field),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.NotEq {
		operator := queryPB.FilterOperator_FILTER_OPERATOR_NOT_EQUALS
		if len(values) > 1 {
			operator = queryPB.FilterOperator_FILTER_OPERATOR_NOT_IN
		}
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: operator,
			Values:   convertValuesToProtobufWithType(values, field),
		}
		filters = append(filters, filter)
	}

	for column, value := range params.Gt {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
			Values:   convertValuesToProtobufWithType([]any{value}, field),
		}
		filters = append(filters, filter)
	}

	for column, value := range params.Gte {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN_OR_EQUAL,
			Values:   convertValuesToProtobufWithType([]any{value}, field),
		}
		filters = append(filters, filter)
	}

	for column, value := range params.Lt {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN,
			Values:   convertValuesToProtobufWithType([]any{value}, field),
		}
		filters = append(filters, filter)
	}

	for column, value := range params.Lte {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN_OR_EQUAL,
			Values:   convertValuesToProtobufWithType([]any{value}, field),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.Between {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN,
			Values:   convertValuesToProtobufWithType([]any{values[0], values[1]}, field),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.In {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_IN,
			Values:   convertValuesToProtobufWithType(values, field),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.NotIn {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_NOT_IN,
			Values:   convertValuesToProtobufWithType(values, field),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.Like {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_LIKE,
			Values:   convertValuesToProtobufWithType(convertStringSliceToAny(values), field),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.ILike {
		field := getFieldDescriptor(fd, column)
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
			Values:   convertValuesToProtobufWithType(convertStringSliceToAny(values), field),
		}
		filters = append(filters, filter)
	}

	for _, column := range params.IsNull {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL,
			Values:   []*queryPB.FilterValue{},
		}
		filters = append(filters, filter)
	}

	for _, column := range params.IsNotNull {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_NULL,
			Values:   []*queryPB.FilterValue{},
		}
		filters = append(filters, filter)
	}

	filters = append(filters, convertJSONFilters(params, fd)...)
	filters = append(filters, convertArrayFilters(params, fd)...)

	return filters
}

func convertFilterGroupsToProtobuf(params *PaginationParams) []*queryPB.FilterGroup {
	var filterGroups []*queryPB.FilterGroup

	for _, group := range params.FilterGroups {
		if group == nil {
			continue
		}
		pbGroup := &queryPB.FilterGroup{
			Operator: group.Operator,
			Filters:  group.Filters,
		}
		filterGroups = append(filterGroups, pbGroup)
	}

	return filterGroups
}

func convertJSONFilters(params *PaginationParams, fd protoreflect.MessageDescriptor) []*queryPB.Filter {
	var filters []*queryPB.Filter

	for column, values := range params.JSONContains {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_JSON_CONTAINS,
			Values:   convertValuesToProtobufWithType(convertStringSliceToAny(values), getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.JSONExists {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_JSON_EXISTS,
			Values:   convertValuesToProtobufWithType(convertStringSliceToAny(values), getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.JSONAnyExists {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_JSON_ANY_EXISTS,
			Values:   convertValuesToProtobufWithType(convertStringSliceToAny(values), getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.JSONAllExists {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_JSON_ALL_EXISTS,
			Values:   convertValuesToProtobufWithType(convertStringSliceToAny(values), getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.JSONPathQuery {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_JSON_PATH,
			Values:   convertValuesToProtobufWithType(convertStringSliceToAny(values), getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	return filters
}

func convertArrayFilters(params *PaginationParams, fd protoreflect.MessageDescriptor) []*queryPB.Filter {
	var filters []*queryPB.Filter

	for column, values := range params.ArrayContains {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_ARRAY_CONTAINS,
			Values:   convertValuesToProtobufWithType(values, getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.ArrayContainedBy {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_ARRAY_CONTAINED_BY,
			Values:   convertValuesToProtobufWithType(values, getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	for column, values := range params.ArrayOverlap {
		filter := &queryPB.Filter{
			Column:   column,
			Operator: queryPB.FilterOperator_FILTER_OPERATOR_ARRAY_OVERLAP,
			Values:   convertValuesToProtobufWithType(values, getFieldDescriptor(fd, column)),
		}
		filters = append(filters, filter)
	}

	return filters
}

func convertStringSliceToAny(values []string) []any {
	result := make([]any, len(values))
	for i, v := range values {
		result[i] = v
	}
	return result
}

func getFieldDescriptor(fd protoreflect.MessageDescriptor, columnName string) protoreflect.FieldDescriptor {
	if fd == nil {
		return nil
	}
	info, err := queryField(fd, columnName)
	if err != nil {
		return nil
	}
	return info.Descriptor
}

func convertValuesToProtobufWithType(values []any, field protoreflect.FieldDescriptor) []*queryPB.FilterValue {
	var filterValues []*queryPB.FilterValue

	for _, value := range values {
		filterValue := &queryPB.FilterValue{}

		// If we have field descriptor information, use it for type-aware conversion
		if field != nil {
			convertValueToFilterValueWithType(value, field, filterValue)
		} else {
			// Fallback to original conversion logic
			convertValueToFilterValueLegacy(value, filterValue)
		}

		filterValues = append(filterValues, filterValue)
	}

	return filterValues
}

// Preserve values outside the signed protobuf field's range as decimal strings.
func setUnsignedFilterValue(value uint64, filterValue *queryPB.FilterValue) {
	if value > math.MaxInt64 {
		filterValue.StringValue = strconv.FormatUint(value, 10)
		return
	}
	filterValue.IntValue = int64(value)
}

func convertValueToFilterValueWithType(value any, field protoreflect.FieldDescriptor, filterValue *queryPB.FilterValue) {
	switch field.Kind() {
	case protoreflect.BytesKind:
		if str, ok := value.(string); ok {
			// Try base64 decode first for bytes fields
			if bytes, err := base64.StdEncoding.DecodeString(str); err == nil {
				filterValue.BytesValue = bytes
			} else {
				// Fallback to raw bytes
				filterValue.BytesValue = []byte(str)
			}
		} else if bytes, ok := value.([]byte); ok {
			filterValue.BytesValue = bytes
		}
	case protoreflect.StringKind:
		filterValue.StringValue = fmt.Sprintf("%v", value)
	case protoreflect.Int64Kind, protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind, protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		if str, ok := value.(string); ok {
			if intVal, err := strconv.ParseInt(str, 10, 64); err == nil {
				filterValue.IntValue = intVal
			} else {
				filterValue.StringValue = str
			}
		} else if intVal, ok := value.(int64); ok {
			filterValue.IntValue = intVal
		} else if intVal, ok := value.(int); ok {
			filterValue.IntValue = int64(intVal)
		}
	case protoreflect.Uint64Kind, protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		if str, ok := value.(string); ok {
			if intVal, err := strconv.ParseUint(str, 10, 64); err == nil {
				setUnsignedFilterValue(intVal, filterValue)
			} else {
				filterValue.StringValue = str
			}
		} else if intVal, ok := value.(uint64); ok {
			setUnsignedFilterValue(intVal, filterValue)
		} else if intVal, ok := value.(uint); ok {
			setUnsignedFilterValue(uint64(intVal), filterValue)
		}
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		if str, ok := value.(string); ok {
			if floatVal, err := strconv.ParseFloat(str, 64); err == nil {
				filterValue.DoubleValue = floatVal
			} else {
				filterValue.StringValue = str
			}
		} else if floatVal, ok := value.(float64); ok {
			filterValue.DoubleValue = floatVal
		} else if floatVal, ok := value.(float32); ok {
			filterValue.DoubleValue = float64(floatVal)
		}
	case protoreflect.BoolKind:
		if str, ok := value.(string); ok {
			if boolVal, err := strconv.ParseBool(str); err == nil {
				filterValue.BoolValue = boolVal
			} else {
				filterValue.StringValue = str
			}
		} else if boolVal, ok := value.(bool); ok {
			filterValue.BoolValue = boolVal
		}
	case protoreflect.MessageKind:
		// Handle well-known message types
		switch field.Message().FullName() {
		case "google.protobuf.Timestamp":
			if str, ok := value.(string); ok {
				if ts, err := time.Parse(time.RFC3339, str); err == nil {
					filterValue.TimestampValue = timestamppb.New(ts)
				} else {
					filterValue.StringValue = str
				}
			} else if ts, ok := value.(time.Time); ok {
				filterValue.TimestampValue = timestamppb.New(ts)
			} else if ts, ok := value.(*timestamppb.Timestamp); ok {
				filterValue.TimestampValue = ts
			}
		case "google.protobuf.Duration":
			if str, ok := value.(string); ok {
				if dur, err := time.ParseDuration(str); err == nil {
					filterValue.DurationValue = durationpb.New(dur)
				} else {
					filterValue.StringValue = str
				}
			} else if dur, ok := value.(time.Duration); ok {
				filterValue.DurationValue = durationpb.New(dur)
			} else if dur, ok := value.(*durationpb.Duration); ok {
				filterValue.DurationValue = dur
			}
		default:
			// For other message types, convert to string
			filterValue.StringValue = fmt.Sprintf("%v", value)
		}
	default:
		// For enum and other types, try string conversion
		filterValue.StringValue = fmt.Sprintf("%v", value)
	}
}

func convertValueToFilterValueLegacy(value any, filterValue *queryPB.FilterValue) {
	switch v := value.(type) {
	case string:
		filterValue.StringValue = v
	case int64:
		filterValue.IntValue = v
	case float64:
		filterValue.DoubleValue = v
	case bool:
		filterValue.BoolValue = v
	case *timestamppb.Timestamp:
		filterValue.TimestampValue = v
	case time.Time:
		filterValue.TimestampValue = timestamppb.New(v)
	case *durationpb.Duration:
		filterValue.DurationValue = v
	case time.Duration:
		filterValue.DurationValue = durationpb.New(v)
	case []byte:
		filterValue.BytesValue = v
	default:
		// try to convert time.Time to timestamp
		if t, ok := value.(interface{ Time() *timestamppb.Timestamp }); ok {
			filterValue.TimestampValue = t.Time()
		} else if t, ok := value.(interface{ Unix() int64 }); ok {
			// handle time.Time via Unix timestamp
			filterValue.TimestampValue = timestamppb.New(time.Unix(t.Unix(), 0))
		} else if d, ok := value.(interface{ Duration() *durationpb.Duration }); ok {
			// handle duration via Duration() method
			filterValue.DurationValue = d.Duration()
		} else {
			// for other types, try to convert to string
			filterValue.StringValue = fmt.Sprintf("%v", v)
		}
	}
}

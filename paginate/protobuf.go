package paginate

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// DBConn accepts a pgx connection, pool, or caller-owned transaction.
type DBConn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

func ProtoPaginate[T proto.Message, M any](ctx context.Context, pgxConn DBConn, logger *slog.Logger, tableName string, query *queryPB.PaginationRequest, convertFn func(*M) (T, error)) ([]T, *queryPB.PaginationResponse, error) {
	return protoPaginateWithRowConverter[T, M](ctx, pgxConn, logger, tableName, query, func(rows pgx.Rows) ([]T, []map[string]any, error) {
		models, err := ScanRows[M](rows)
		if err != nil {
			return nil, nil, err
		}

		// convert model structs to protobuf messages
		protoModels := make([]T, len(models))
		for i, model := range models {
			protoModel, err := convertFn(&model)
			if err != nil {
				return nil, nil, oops.In(ServiceName).Wrapf(err, "converting model to proto")
			}
			protoModels[i] = protoModel
		}
		cursorRows, err := cursorRowValues(models, nil, query.GetWithTokenPagination())
		return protoModels, cursorRows, err
	}, nil)
}

// GetPaginationInfo returns pagination metadata without executing any queries
func GetPaginationInfo[T proto.Message]() (*queryPB.PaginationResponse, error) {
	protoType, err := CreateProtoInstance[T]()
	if err != nil {
		return nil, oops.In(ServiceName).Wrapf(err, "creating proto instance")
	}
	return buildPaginationInfoResponse(protoType)
}

// GetPaginationInfoForModel restricts metadata to fields represented by M.
// GetPaginationInfo only describes the protobuf schema and its annotations.
func GetPaginationInfoForModel[T proto.Message, M any]() (*queryPB.PaginationResponse, error) {
	protoType, err := CreateProtoInstance[T]()
	if err != nil {
		return nil, err
	}
	var model M
	columns, err := GetColumnNames(model)
	if err != nil {
		return nil, err
	}
	return buildPaginationInfoResponse(protoType, columns)
}

// ProtoPaginateExtended extends ProtoPaginate to support custom joins while keeping
// automatic field selection from the model and using existing model-to-proto converters.
func ProtoPaginateExtended[T proto.Message, M any](
	ctx context.Context,
	pgxConn DBConn,
	logger *slog.Logger,
	tableName string,
	query *queryPB.PaginationRequest,
	convertFn func(*M, map[string]any) (T, error), // Enhanced converter with additional data
	joinConfig *ExtendedJoinConfig,
) ([]T, *queryPB.PaginationResponse, error) {
	return protoPaginateWithRowConverter[T, M](ctx, pgxConn, logger, tableName, query, func(rows pgx.Rows) ([]T, []map[string]any, error) {
		// Use ScanRowsWithAdditional to handle both model fields and additional join columns
		var additionalColumns []string
		var additionalColumnTypes map[string]any
		if joinConfig != nil {
			additionalColumns = joinConfig.AdditionalColumns
			additionalColumnTypes = joinConfig.AdditionalColumnTypes
		}

		// Store additional data for each row to pass to converter
		var allAdditionalData []map[string]any

		models, err := ScanRowsWithAdditional[M](rows, additionalColumns, additionalColumnTypes, func(additionalData map[string]any) error {
			// Store additional data for this row
			allAdditionalData = append(allAdditionalData, additionalData)
			return nil
		})
		if err != nil {
			return nil, nil, err
		}

		// Convert using the enhanced converter function with additional data
		protoModels := make([]T, len(models))
		for i, model := range models {
			var additionalData map[string]any
			if i < len(allAdditionalData) {
				additionalData = allAdditionalData[i]
			}
			protoModel, err := convertFn(&model, additionalData)
			if err != nil {
				return nil, nil, oops.In(ServiceName).Wrapf(err, "converting model to proto")
			}
			protoModels[i] = protoModel
		}
		cursorRows, err := cursorRowValues(models, allAdditionalData, query.GetWithTokenPagination())
		return protoModels, cursorRows, err
	}, joinConfig)
}

// protoPaginateWithRowConverter is the shared core implementation for both ProtoPaginate and ProtoPaginateExtended
func protoPaginateWithRowConverter[T proto.Message, M any](
	ctx context.Context,
	pgxConn DBConn,
	logger *slog.Logger,
	tableName string,
	query *queryPB.PaginationRequest,
	convertFn func(pgx.Rows) ([]T, []map[string]any, error),
	joinConfig *ExtendedJoinConfig,
) ([]T, *queryPB.PaginationResponse, error) {
	paginationParams, err := ConvertProtoRequest[T](query)
	if err != nil {
		return nil, nil, err
	}

	// The protobuf may contain hydrated fields that have no database column.
	var modelType M
	modelInstance, err := CreateModelInstance(modelType)
	if err != nil {
		return nil, nil, oops.In(ServiceName).Wrapf(err, "creating model instance")
	}
	columns, err := GetColumnNames(modelInstance)
	if err != nil {
		return nil, nil, err
	}
	if joinConfig != nil {
		for _, expression := range joinConfig.AdditionalColumns {
			columns = append(columns, extractColumnAlias(expression), selectedColumnSource(expression))
		}
	}
	if paginationParams.WithPaginationInfoOnly {
		protoType, err := CreateProtoInstance[T]()
		if err != nil {
			return nil, nil, err
		}
		paginationInfo, err := buildPaginationInfoResponse(protoType, columns)
		return nil, paginationInfo, err
	}

	builder := NewBuilder().
		Table(tableName).
		Model(modelInstance).
		FromPaginationParams(paginationParams)

	// Add custom joins and additional columns if provided
	if joinConfig != nil {
		// Add custom joins from config
		for _, join := range joinConfig.Joins {
			builder.Join(join)
		}
		if joinConfig.GroupBy != "" {
			builder.GroupBy(joinConfig.GroupBy)
		}

		if len(joinConfig.AdditionalColumnTypes) > 0 { // Needed for fk uuids
			builder.AdditionalColumnTypes(joinConfig.AdditionalColumnTypes)
		}

		// Add additional columns to the automatic model field selection
		if len(joinConfig.AdditionalColumns) > 0 {
			builder.AdditionalSelect(joinConfig.AdditionalColumns...)
		}
	}

	builtParams, err := builder.Build()
	if err != nil {
		return nil, nil, err
	}

	listQuery, listArgs, err := builder.BuildSQL()
	if err != nil {
		return nil, nil, err
	}
	if logger != nil {
		logger.DebugContext(ctx, "pagination query", "sql", listQuery)
	}

	// skip building (and running) the count query entirely when the caller
	// opted out of counts; its args would only be appended to the shared list
	var countQuery string
	var countArgs []any
	if query == nil || !query.WithoutCount {
		countQuery, countArgs, err = builder.BuildCountSQL()
		if err != nil {
			return nil, nil, err
		}
		if logger != nil {
			logger.DebugContext(ctx, "pagination count query", "sql", countQuery)
		}
	}

	// Reuse a caller-supplied transaction without committing or rolling it back.
	// Otherwise, own a new transaction for the list and count queries.
	tx, ownsTx, err := beginTx(ctx, pgxConn)
	if err != nil {
		return nil, nil, err
	}
	if ownsTx {
		defer func(tx pgx.Tx, ctx context.Context) {
			_ = tx.Rollback(ctx)
		}(tx, ctx)
	}

	rows, err := tx.Query(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	// Convert rows using the provided conversion function
	protoModels, cursorRows, err := convertFn(rows)
	if err != nil {
		return nil, nil, err
	}

	var countRows pgx.Rows
	if query == nil || !query.WithoutCount {
		countRows, err = tx.Query(ctx, countQuery, countArgs...)
		if err != nil {
			return nil, nil, err
		}
		defer countRows.Close()
	}

	// Tokens must use database row values, not converted protobuf field names.
	responseParams := *builtParams
	responseParams.WithTokenPagination = false
	protoModels, resp, err := CreatePaginationResponse(protoModels, countRows, &responseParams)
	if err != nil {
		return nil, nil, err
	}
	if builtParams.WithTokenPagination && resp.HasMore && len(protoModels) > 0 {
		resp.NextPageToken, err = GenerateNextTokenFromModel(cursorRows[len(protoModels)-1], builtParams)
		if err != nil {
			return nil, nil, err
		}
	}
	if ownsTx {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
	}

	return protoModels, resp, nil
}

// cursorRowValues retains database column names through protobuf conversion.
func cursorRowValues[M any](models []M, additional []map[string]any, enabled bool) ([]map[string]any, error) {
	if !enabled {
		return nil, nil
	}
	rows := make([]map[string]any, len(models))
	for i, model := range models {
		columns, err := GetColumnNames(model)
		if err != nil {
			return nil, err
		}
		rows[i], err = ExtractModelValues(model, columns)
		if err != nil {
			return nil, err
		}
		if i < len(additional) {
			maps.Copy(rows[i], additional[i])
		}
	}
	return rows, nil
}

// beginTx reuses an explicit pgx.Tx, otherwise it opens a new transaction.
func beginTx(ctx context.Context, pgxConn DBConn) (pgx.Tx, bool, error) {
	if tx, ok := pgxConn.(pgx.Tx); ok {
		return tx, false, nil
	}
	tx, err := pgxConn.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	return tx, true, nil
}

// ConvertProtoRequest converts a PaginationRequest to PaginationParams with validation
func ConvertProtoRequest[T proto.Message](
	req *queryPB.PaginationRequest,
) (*PaginationParams, error) {
	params := &PaginationParams{
		PageSize: DefaultLimit,
		// Initialize all maps to prevent nil pointer issues
		Eq:         make(map[string][]any),
		NotEq:      make(map[string][]any),
		Gt:         make(map[string]any),
		Gte:        make(map[string]any),
		Lt:         make(map[string]any),
		Lte:        make(map[string]any),
		Between:    make(map[string][2]any),
		In:         make(map[string][]any),
		NotIn:      make(map[string][]any),
		Like:       make(map[string][]string),
		ILike:      make(map[string][]string),
		IsNull:     make([]string, 0),
		IsNotNull:  make([]string, 0),
		IsEmpty:    make([]string, 0),
		IsNotEmpty: make([]string, 0),
		// JSON Filtering
		JSONContains:  make(map[string][]string),
		JSONExists:    make(map[string][]string),
		JSONAnyExists: make(map[string][]string),
		JSONAllExists: make(map[string][]string),
		JSONPathQuery: make(map[string][]string),
		// Array Filtering
		ArrayContains:    make(map[string][]any),
		ArrayContainedBy: make(map[string][]any),
		ArrayOverlap:     make(map[string][]any),
		// Sorting
		Sort: make([]*SortEntry, 0),
		// Filter Groups
		FilterGroups: make([]*FilterGroup, 0),
		// Extra Settings
		CursorColumns: make([]string, 0),
		// Internal slices
		selectColumns: make([]string, 0),
		whereClauses:  make([]string, 0),
		whereArgs:     make([]any, 0),
		joins:         make([]string, 0),
	}

	if req != nil {
		if req.PageSize > 0 && req.PageSize <= MaxLimit {
			params.PageSize = req.PageSize
		}
		params.Skip = req.Skip
		params.PageToken = req.PageToken
		params.WithoutCount = req.WithoutCount
		params.WithCountEstimate = req.WithCountEstimate
		params.WithPaginationInfoOnly = req.WithPaginationInfoOnly
		params.WithTokenPagination = req.WithTokenPagination

		// create a zero value to get the message descriptor
		var m T
		fd := m.ProtoReflect().Type().Descriptor()

		// convert sort entries with validation
		if err := convertSortEntries(req.Sort, params, fd); err != nil {
			return nil, err
		}

		// convert filters with validation
		if err := convertFilters(req.Filters, params, fd); err != nil {
			return nil, err
		}

		// Convert filter groups to the internal format.
		if err := convertFilterGroups(req.FilterGroups, params, fd); err != nil {
			return nil, err
		}
	}

	return params, nil
}

func convertSortEntries(
	sortEntries []*queryPB.SortEntry,
	params *PaginationParams,
	fd protoreflect.MessageDescriptor,
) error {
	for _, entry := range sortEntries {
		if entry == nil {
			return oops.In(ServiceName).New("sort entry is required")
		}
		info, err := queryField(fd, entry.Column)
		if err != nil {
			return err
		}
		field := info.Descriptor

		if !isFieldSortable(field) {
			return oops.In(ServiceName).Errorf("field '%s' is not sortable", entry.Column)
		}

		if !isSortOperatorAllowed(field, sortEntryToOperator(entry)) {
			return oops.In(ServiceName).Errorf("operator %v not allowed for field '%s'", entry.Order, entry.Column)
		}

		// create SortEntry
		sortEntry := &SortEntry{
			Column: info.Column,
			Order:  entry.Order,
		}

		// handle advanced sorting options
		if entry.JsonPath != "" {
			sortEntry.JSONPath = entry.JsonPath
		}
		if entry.JsonText != "" {
			sortEntry.JSONText = entry.JsonText
		}
		if entry.JsonNumeric != "" {
			sortEntry.JSONNumeric = entry.JsonNumeric
		}
		if entry.ArrayIndex != 0 { // pg arrays start at 1
			sortEntry.ArrayIndex = entry.ArrayIndex
		}
		if entry.ArrayLength {
			sortEntry.ArrayLength = entry.ArrayLength
		}
		if entry.ArrayContains != "" {
			sortEntry.ArrayContains = []any{entry.ArrayContains}
		}

		params.Sort = append(params.Sort, sortEntry)
	}

	return nil
}

func convertFilters(
	filters []*queryPB.Filter,
	params *PaginationParams,
	fd protoreflect.MessageDescriptor,
) error {
	// Each entry is a separate predicate. Keep repeated column/operator pairs
	// in individual AND groups instead of merging or overwriting them.
	type filterKey struct {
		column   string
		operator queryPB.FilterOperator
	}
	seen := make(map[filterKey]bool)
	for _, filter := range filters {
		if filter == nil || filter.Column == "" {
			return oops.In(ServiceName).New("filter and column are required")
		}
		if fd != nil {
			info, err := queryField(fd, filter.Column)
			if err != nil {
				return err
			}
			field := info.Descriptor
			if !isFieldFilterable(field) {
				return oops.In(ServiceName).Errorf("field '%s' is not filterable", filter.Column)
			}
			if !isFilterOperatorAllowed(field, filter.Operator) {
				return oops.In(ServiceName).Errorf("operator %v not allowed for field '%s'", filter.Operator, filter.Column)
			}
			filter = mapFilter(filter, info)
		}
		filterValues := filter.Values
		if filterIgnoresValues(filter.Operator) {
			filterValues = nil
		}
		values, err := convertFilterValues(filterValues)
		if err != nil {
			return oops.In(ServiceName).Wrapf(err, "converting filter values for field '%s'", filter.Column)
		}
		if err := validateFilterArity(filter.Operator, len(values)); err != nil {
			return err
		}
		key := filterKey{filter.Column, filter.Operator}
		if seen[key] {
			params.FilterGroups = append(params.FilterGroups, &FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_AND,
				Filters:  []*queryPB.Filter{filter},
			})
			continue
		}
		if err := addFilterToParams(filter, values, params); err != nil {
			return oops.In(ServiceName).Wrapf(err, "adding filter for field '%s'", filter.Column)
		}
		seen[key] = true
	}

	return nil
}

func convertFilterValues(values []*queryPB.FilterValue) ([]any, error) {
	result := make([]any, len(values))

	for i, value := range values {
		if value == nil {
			return nil, oops.In(ServiceName).Errorf("nil filter value at index %d", i)
		}
		switch {
		case value.StringValue != "":
			result[i] = value.StringValue
		case value.IntValue != 0:
			result[i] = value.IntValue
		case value.DoubleValue != 0:
			result[i] = value.DoubleValue
		case value.BoolValue:
			result[i] = value.BoolValue
		case value.TimestampValue != nil:
			if err := value.TimestampValue.CheckValid(); err != nil {
				return nil, err
			}
			result[i] = value.TimestampValue.AsTime()
		case value.DurationValue != nil:
			if err := value.DurationValue.CheckValid(); err != nil {
				return nil, err
			}
			result[i] = value.DurationValue.AsDuration()
		case value.BytesValue != nil:
			result[i] = value.BytesValue
		default:
			return nil, oops.In(ServiceName).Errorf("empty filter value at index %d", i)
		}
	}

	return result, nil
}

func addFilterToParams(filter *queryPB.Filter, values []any, params *PaginationParams) error {
	switch filter.Operator {
	case queryPB.FilterOperator_FILTER_OPERATOR_EQUALS:
		params.Eq[filter.Column] = append(params.Eq[filter.Column], values...)
	case queryPB.FilterOperator_FILTER_OPERATOR_NOT_EQUALS:
		params.NotEq[filter.Column] = append(params.NotEq[filter.Column], values...)
	case queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN:
		if len(values) != 1 {
			return oops.In(ServiceName).New("GREATER_THAN requires exactly 1 value")
		}
		params.Gt[filter.Column] = values[0]
	case queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN_OR_EQUAL:
		if len(values) != 1 {
			return oops.In(ServiceName).New("GREATER_THAN_OR_EQUAL requires exactly 1 value")
		}
		params.Gte[filter.Column] = values[0]
	case queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN:
		if len(values) != 1 {
			return oops.In(ServiceName).New("LESS_THAN requires exactly 1 value")
		}
		params.Lt[filter.Column] = values[0]
	case queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN_OR_EQUAL:
		if len(values) != 1 {
			return oops.In(ServiceName).New("LESS_THAN_OR_EQUAL requires exactly 1 value")
		}
		params.Lte[filter.Column] = values[0]
	case queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN:
		if len(values) != 2 {
			return oops.In(ServiceName).New("BETWEEN requires exactly 2 values")
		}
		params.Between[filter.Column] = [2]any{values[0], values[1]}
	case queryPB.FilterOperator_FILTER_OPERATOR_IN:
		params.In[filter.Column] = append(params.In[filter.Column], values...)
	case queryPB.FilterOperator_FILTER_OPERATOR_NOT_IN:
		params.NotIn[filter.Column] = append(params.NotIn[filter.Column], values...)
	case queryPB.FilterOperator_FILTER_OPERATOR_LIKE:
		strValues := make([]string, len(values))
		for i, v := range values {
			if str, ok := v.(string); ok {
				strValues[i] = str
			} else {
				return oops.In(ServiceName).New("LIKE operator requires string values")
			}
		}
		params.Like[filter.Column] = append(params.Like[filter.Column], strValues...)
	case queryPB.FilterOperator_FILTER_OPERATOR_ILIKE:
		strValues := make([]string, len(values))
		for i, v := range values {
			if str, ok := v.(string); ok {
				strValues[i] = str
			} else {
				return oops.In(ServiceName).New("ILIKE operator requires string values")
			}
		}
		params.ILike[filter.Column] = append(params.ILike[filter.Column], strValues...)
	case queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL:
		params.IsNull = append(params.IsNull, filter.Column)
	case queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_NULL:
		params.IsNotNull = append(params.IsNotNull, filter.Column)
	case queryPB.FilterOperator_FILTER_OPERATOR_IS_EMPTY:
		params.IsEmpty = append(params.IsEmpty, filter.Column)
	case queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_EMPTY:
		params.IsNotEmpty = append(params.IsNotEmpty, filter.Column)
	case queryPB.FilterOperator_FILTER_OPERATOR_JSON_CONTAINS:
		strValues := make([]string, len(values))
		for i, v := range values {
			if str, ok := v.(string); ok {
				strValues[i] = str
			} else {
				return oops.In(ServiceName).New("JSON_CONTAINS operator requires string values")
			}
		}
		params.JSONContains[filter.Column] = append(params.JSONContains[filter.Column], strValues...)
	case queryPB.FilterOperator_FILTER_OPERATOR_JSON_EXISTS:
		strValues := make([]string, len(values))
		for i, v := range values {
			if str, ok := v.(string); ok {
				strValues[i] = str
			} else {
				return oops.In(ServiceName).New("JSON_EXISTS operator requires string values")
			}
		}
		params.JSONExists[filter.Column] = append(params.JSONExists[filter.Column], strValues...)
	case queryPB.FilterOperator_FILTER_OPERATOR_JSON_ANY_EXISTS:
		strValues := make([]string, len(values))
		for i, v := range values {
			if str, ok := v.(string); ok {
				strValues[i] = str
			} else {
				return oops.In(ServiceName).New("JSON_ANY_EXISTS operator requires string values")
			}
		}
		params.JSONAnyExists[filter.Column] = append(params.JSONAnyExists[filter.Column], strValues...)
	case queryPB.FilterOperator_FILTER_OPERATOR_JSON_ALL_EXISTS:
		strValues := make([]string, len(values))
		for i, v := range values {
			if str, ok := v.(string); ok {
				strValues[i] = str
			} else {
				return oops.In(ServiceName).New("JSON_ALL_EXISTS operator requires string values")
			}
		}
		params.JSONAllExists[filter.Column] = append(params.JSONAllExists[filter.Column], strValues...)
	case queryPB.FilterOperator_FILTER_OPERATOR_JSON_PATH:
		strValues := make([]string, len(values))
		for i, v := range values {
			if str, ok := v.(string); ok {
				strValues[i] = str
			} else {
				return oops.In(ServiceName).New("JSON_PATH operator requires string values")
			}
		}
		params.JSONPathQuery[filter.Column] = append(params.JSONPathQuery[filter.Column], strValues...)
	case queryPB.FilterOperator_FILTER_OPERATOR_ARRAY_CONTAINS:
		params.ArrayContains[filter.Column] = append(params.ArrayContains[filter.Column], values...)
	case queryPB.FilterOperator_FILTER_OPERATOR_ARRAY_CONTAINED_BY:
		params.ArrayContainedBy[filter.Column] = append(params.ArrayContainedBy[filter.Column], values...)
	case queryPB.FilterOperator_FILTER_OPERATOR_ARRAY_OVERLAP:
		params.ArrayOverlap[filter.Column] = append(params.ArrayOverlap[filter.Column], values...)
	default:
		return oops.In(ServiceName).Errorf("unsupported filter operator: %v", filter.Operator)
	}

	return nil
}

func convertFilterGroups(
	filterGroups []*queryPB.FilterGroup,
	params *PaginationParams,
	fd protoreflect.MessageDescriptor,
) error {
	for _, group := range filterGroups {
		if group == nil {
			continue
		}
		if err := convertFilters(group.Filters, NewPaginationParams(), fd); err != nil {
			return err
		}
		mapped := make([]*queryPB.Filter, len(group.Filters))
		for i, filter := range group.Filters {
			if fd == nil {
				mapped[i] = proto.CloneOf(filter)
				continue
			}
			info, err := queryField(fd, filter.Column)
			if err != nil {
				return err
			}
			mapped[i] = mapFilter(filter, info)
		}
		params.FilterGroups = append(params.FilterGroups, &FilterGroup{
			Operator: group.Operator,
			Filters:  mapped,
		})
	}
	return nil
}

// filterIgnoresValues matches PostgreSQL's unary null and empty predicates.
func filterIgnoresValues(operator queryPB.FilterOperator) bool {
	switch operator {
	case queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL,
		queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_NULL,
		queryPB.FilterOperator_FILTER_OPERATOR_IS_EMPTY,
		queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_EMPTY:
		return true
	default:
		return false
	}
}

// validateFilterArity prevents malformed predicates from being silently dropped.
func validateFilterArity(operator queryPB.FilterOperator, count int) error {
	if filterIgnoresValues(operator) {
		return nil
	}
	switch operator {
	case queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN:
		if count == 2 {
			return nil
		}
	case queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
		queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN_OR_EQUAL,
		queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN,
		queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN_OR_EQUAL:
		if count == 1 {
			return nil
		}
	default:
		if count > 0 {
			return nil
		}
	}
	return oops.In(ServiceName).Errorf("invalid value count %d for operator %v", count, operator)
}

func isFieldSortable(field protoreflect.FieldDescriptor) bool {
	opts := field.Options()
	if opts == nil {
		return true // allow querying unless explicitly disabled
	}

	// check for our custom sortable option
	if proto.HasExtension(opts, queryPB.E_Sortable) {
		if sortable := proto.GetExtension(opts, queryPB.E_Sortable); sortable != nil {
			if val, ok := sortable.(bool); ok {
				return val // use explicit setting if provided
			}
		}
	}

	return true // allow querying unless explicitly disabled
}

func isFieldFilterable(field protoreflect.FieldDescriptor) bool {
	opts := field.Options()
	if opts == nil {
		return true // allow querying unless explicitly disabled
	}

	// check for our custom filterable option
	if proto.HasExtension(opts, queryPB.E_Filterable) {
		if filterable := proto.GetExtension(opts, queryPB.E_Filterable); filterable != nil {
			if val, ok := filterable.(bool); ok {
				return val // use explicit setting if provided
			}
		}
	}

	return true // allow querying unless explicitly disabled
}

func sortEntryToOperator(sortEntry *queryPB.SortEntry) queryPB.SortOperator {
	if sortEntry.JsonPath != "" {
		return queryPB.SortOperator_SORT_OPERATOR_JSON_PATH
	}
	if sortEntry.JsonText != "" {
		return queryPB.SortOperator_SORT_OPERATOR_JSON_TEXT
	}
	if sortEntry.JsonNumeric != "" {
		return queryPB.SortOperator_SORT_OPERATOR_JSON_NUMERIC
	}
	if sortEntry.ArrayIndex != 0 {
		return queryPB.SortOperator_SORT_OPERATOR_ARRAY_INDEX
	}
	if sortEntry.ArrayLength {
		return queryPB.SortOperator_SORT_OPERATOR_ARRAY_LENGTH
	}
	if sortEntry.ArrayContains != "" {
		return queryPB.SortOperator_SORT_OPERATOR_ARRAY_CONTAINS
	}
	return queryPB.SortOperator_SORT_OPERATOR_STANDARD
}

func isSortOperatorAllowed(field protoreflect.FieldDescriptor, operator queryPB.SortOperator) bool {
	opts := field.Options()
	if opts == nil {
		return true // allow all operators by default
	}

	// get allowed operators from extension
	if proto.HasExtension(opts, queryPB.E_AllowedSortOperators) {
		if allowedOps := proto.GetExtension(opts, queryPB.E_AllowedSortOperators); allowedOps != nil {
			// Check if the extension value is a slice of SortOperator
			if operators, ok := allowedOps.([]queryPB.SortOperator); ok {
				// if specific operators are listed, check against them
				return slices.Contains(operators, operator) // operator not in the allowed list
			}
			// Check if it's a single SortOperator
			if singleOp, ok := allowedOps.(queryPB.SortOperator); ok {
				return singleOp == operator
			}
		}
	}

	// if no specific operators listed, allow all operators (subject to field being sortable)
	return isFieldSortable(field)
}

func isFilterOperatorAllowed(field protoreflect.FieldDescriptor, operator queryPB.FilterOperator) bool {
	opts := field.Options()
	if opts == nil {
		return true // allow all operators by default
	}

	// get allowed operators from extension
	if proto.HasExtension(opts, queryPB.E_AllowedFilterOperators) {
		if allowedOps := proto.GetExtension(opts, queryPB.E_AllowedFilterOperators); allowedOps != nil {
			// Check if the extension value is a slice of FilterOperator
			if operators, ok := allowedOps.([]queryPB.FilterOperator); ok {
				// if specific operators are listed, check against them
				return slices.Contains(operators, operator) // operator not in the allowed list
			}
			// Check if it's a single FilterOperator
			if singleOp, ok := allowedOps.(queryPB.FilterOperator); ok {
				return singleOp == operator
			}
		}
	}

	// if no specific operators listed, allow all operators (subject to field being filterable)
	return isFieldFilterable(field)
}

// buildPaginationInfoResponse creates a pagination info response using cached field information
func buildPaginationInfoResponse[T proto.Message](protoType T, columnSets ...[]string) (*queryPB.PaginationResponse, error) {
	// Get field information from cache
	fieldInfos := GetFieldInfo(protoType)

	// Convert field infos to pagination info entries
	entries := make([]*queryPB.PaginationInfoEntry, 0, len(fieldInfos))
	for _, info := range fieldInfos {
		entry := &queryPB.PaginationInfoEntry{
			Column:                 info.Name,
			Filterable:             info.Filterable,
			Sortable:               info.Sortable,
			AllowedFilterOperators: info.AllowedFilterOperators,
			AllowedSortOperators:   info.AllowedSortOperators,
			FieldType:              info.FieldType,
			Repeated:               info.Repeated,
		}
		if len(columnSets) > 0 && !hasQueryColumn(columnSets[0], info.Column) {
			entry.Filterable = false
			entry.Sortable = false
			entry.AllowedFilterOperators = nil
			entry.AllowedSortOperators = nil
		}
		entries = append(entries, entry)
	}

	return &queryPB.PaginationResponse{
		PaginationInfo: &queryPB.PaginationInfoResponse{
			Columns: entries,
		},
	}, nil
}

// ExtendedJoinConfig contains configuration for custom joins and additional columns
type ExtendedJoinConfig struct {
	// Joins specifies the JOIN clauses to add to the query
	Joins []string

	// AdditionalColumns specifies extra columns to select from joins (e.g., "certificate_authority_ids", "certificate_profile_ids")
	// These will be added to the automatic model field selection
	AdditionalColumns []string

	// AdditionalColumnTypes optionally specifies concrete scan destination types for additional columns by alias.
	// If omitted for a column, the scanner falls back to interface{}.
	AdditionalColumnTypes map[string]any

	GroupBy string
}

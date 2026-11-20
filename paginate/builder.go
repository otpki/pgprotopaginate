package paginate

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
)

const (
	DefaultLimit = 10
	MaxLimit     = 1000
	DefaultOrder = queryPB.SortOrder_SORT_ORDER_ASC
)

type Builder struct {
	params *PaginationParams
	errs   []error
}

func NewBuilder() *Builder {
	return &Builder{
		params: &PaginationParams{
			PageSize: DefaultLimit,
		},
	}
}

func (b *Builder) Build() (*PaginationParams, error) {
	if err := b.getError(); err != nil {
		return nil, err
	}
	if b.params.table == "" {
		return nil, fmt.Errorf("table is required")
	}
	if b.params.model == nil {
		return nil, fmt.Errorf("model is required")
	}
	return b.params, nil
}

func (b *Builder) BuildSQL() (string, []any, error) {
	_, err := b.Build()
	if err != nil {
		return "", nil, err
	}

	qb := NewQueryBuilder(b.params)
	return qb.BuildSQL()
}

func (b *Builder) BuildCountSQL() (string, []any, error) {
	_, err := b.Build()
	if err != nil {
		return "", nil, err
	}

	qb := NewQueryBuilder(b.params)
	return qb.BuildCountSQL()
}

func (b *Builder) Table(table string) *Builder {
	if table == "" {
		return b.addError(fmt.Errorf("table cannot be empty"))
	}
	b.params.table = table
	return b
}

func (b *Builder) Schema(schema string) *Builder {
	b.params.schema = schema
	return b
}

func (b *Builder) Model(model any) *Builder {
	b.params.model = model
	return b
}

func (b *Builder) AdditionalColumnTypes(types map[string]any) *Builder {
	b.params.additionalColumnTypes = types
	return b
}

func (b *Builder) PageToken(token string) *Builder {
	if len(token) == 0 {
		return b.addError(fmt.Errorf("page token cannot be empty"))
	}
	b.params.PageToken = token
	return b
}

func (b *Builder) PageSize(limit int64) *Builder {
	if limit <= 0 || limit > MaxLimit {
		limit = DefaultLimit
	}
	b.params.PageSize = limit
	return b
}

func (b *Builder) Skip(offset int64) *Builder {
	if offset < 0 {
		offset = 0
	}
	b.params.Skip = offset
	return b
}

func (b *Builder) Select(columns ...string) *Builder {
	b.params.selectColumns = columns
	return b
}

// Add aliased select expressions that can be projected after pagination, saving on subqueries
func (b *Builder) AdditionalSelect(columns ...string) *Builder {
	b.params.additionalSelectColumns = append(b.params.additionalSelectColumns, columns...)
	return b
}

func (b *Builder) Where(clause string, args ...any) *Builder {
	if clause == "" {
		return b.addError(fmt.Errorf("where clause cannot be empty"))
	}
	b.params.whereClauses = append(b.params.whereClauses, clause)
	b.params.whereArgs = append(b.params.whereArgs, args...)
	return b
}

func (b *Builder) Join(clause string) *Builder {
	if clause == "" {
		return b.addError(fmt.Errorf("join clause cannot be empty"))
	}
	b.params.joins = append(b.params.joins, clause)
	return b
}

func (b *Builder) GroupBy(clause string) *Builder {
	if clause == "" {
		return b.addError(fmt.Errorf("group by clause cannot be empty"))
	}
	b.params.groupBy = clause
	return b
}

func (b *Builder) LeftJoin(table, condition string) *Builder {
	return b.Join(fmt.Sprintf("LEFT JOIN %s ON %s", table, condition))
}

func (b *Builder) InnerJoin(table, condition string) *Builder {
	return b.Join(fmt.Sprintf("INNER JOIN %s ON %s", table, condition))
}

func (b *Builder) RightJoin(table, condition string) *Builder {
	return b.Join(fmt.Sprintf("RIGHT JOIN %s ON %s", table, condition))
}

func (b *Builder) Sort(columns ...string) *Builder {
	for _, column := range columns {
		if after, ok := strings.CutPrefix(column, "-"); ok {
			b.SortByDesc(after)
		} else {
			b.SortBy(column)
		}
	}
	return b
}

func (b *Builder) SortBy(column string, direction ...queryPB.SortOrder) *Builder {
	if column == "" {
		return b.addError(fmt.Errorf("sort column cannot be empty"))
	}
	dir := DefaultOrder
	if len(direction) > 0 {
		dir = direction[0]
	}
	b.params.Sort = append(b.params.Sort, &SortEntry{Column: column, Order: dir})
	return b
}

func (b *Builder) SortByDesc(column string) *Builder {
	return b.SortBy(column, queryPB.SortOrder_SORT_ORDER_DESC)
}

func (b *Builder) SortByEntry(entry *SortEntry, direction ...queryPB.SortOrder) *Builder {
	if entry == nil {
		return b.addError(fmt.Errorf("sort entry cannot be nil"))
	}
	if entry.Column == "" {
		return b.addError(fmt.Errorf("sort column cannot be empty"))
	}
	if entry.Order == queryPB.SortOrder_SORT_ORDER_UNSPECIFIED {
		entry.Order = DefaultOrder
	}
	if len(direction) > 0 {
		entry.Order = direction[0]
	}
	b.params.Sort = append(b.params.Sort, entry)
	return b
}

func (b *Builder) SortByJSONPath(column, path string, direction ...queryPB.SortOrder) *Builder {
	return b.SortByEntry(&SortEntry{Column: column, JSONPath: path}, direction...)
}

func (b *Builder) SortByJSONText(column, path string, direction ...queryPB.SortOrder) *Builder {
	return b.SortByEntry(&SortEntry{Column: column, JSONText: path}, direction...)
}

func (b *Builder) SortByJSONNumeric(column, path string, direction ...queryPB.SortOrder) *Builder {
	return b.SortByEntry(&SortEntry{Column: column, JSONNumeric: path}, direction...)
}

func (b *Builder) SortByArrayIndex(column string, index int64, direction ...queryPB.SortOrder) *Builder {
	return b.SortByEntry(&SortEntry{Column: column, ArrayIndex: index}, direction...)
}

func (b *Builder) SortByArrayLength(column string, direction ...queryPB.SortOrder) *Builder {
	return b.SortByEntry(&SortEntry{Column: column, ArrayLength: true}, direction...)
}

func (b *Builder) SortByArrayContains(column string, item any, direction ...queryPB.SortOrder) *Builder {
	return b.SortByEntry(&SortEntry{Column: column, ArrayContains: item}, direction...)
}

func (b *Builder) Eq(column string, values ...any) *Builder {
	return addToMapField(b, &b.params.Eq, column, values...)
}

func (b *Builder) NotEq(column string, values ...any) *Builder {
	return addToMapField(b, &b.params.NotEq, column, values...)
}

func (b *Builder) Gt(column string, value any) *Builder {
	return addToSingleMapField(b, &b.params.Gt, column, value)
}

func (b *Builder) Gte(column string, value any) *Builder {
	return addToSingleMapField(b, &b.params.Gte, column, value)
}

func (b *Builder) Lt(column string, value any) *Builder {
	return addToSingleMapField(b, &b.params.Lt, column, value)
}

func (b *Builder) Lte(column string, value any) *Builder {
	return addToSingleMapField(b, &b.params.Lte, column, value)
}

func (b *Builder) Between(column string, lowerBound, upperBound any) *Builder {
	if b.params.Between == nil {
		b.params.Between = make(map[string][2]any)
	}
	b.params.Between[column] = [2]any{lowerBound, upperBound}
	return b
}

func (b *Builder) In(column string, values ...any) *Builder {
	return addToMapField(b, &b.params.In, column, values...)
}

func (b *Builder) NotIn(column string, values ...any) *Builder {
	return addToMapField(b, &b.params.NotIn, column, values...)
}

func (b *Builder) Like(column string, values ...string) *Builder {
	return addToMapField(b, &b.params.Like, column, values...)
}

func (b *Builder) ILike(column string, values ...string) *Builder {
	return addToMapField(b, &b.params.ILike, column, values...)
}

func (b *Builder) IsNull(column string) *Builder {
	b.params.IsNull = append(b.params.IsNull, column)
	return b
}

func (b *Builder) IsNotNull(column string) *Builder {
	b.params.IsNotNull = append(b.params.IsNotNull, column)
	return b
}

func (b *Builder) IsEmpty(column string) *Builder {
	b.params.IsEmpty = append(b.params.IsEmpty, column)
	return b
}

func (b *Builder) IsNotEmpty(column string) *Builder {
	b.params.IsNotEmpty = append(b.params.IsNotEmpty, column)
	return b
}

func (b *Builder) JSONContains(column string, jsonValues ...string) *Builder {
	return addToMapField(b, &b.params.JSONContains, column, jsonValues...)
}

func (b *Builder) JSONExists(column string, keys ...string) *Builder {
	return addToMapField(b, &b.params.JSONExists, column, keys...)
}

func (b *Builder) JSONAnyExists(column string, keys ...string) *Builder {
	return addToMapField(b, &b.params.JSONAnyExists, column, keys...)
}

func (b *Builder) JSONAllExists(column string, keys ...string) *Builder {
	return addToMapField(b, &b.params.JSONAllExists, column, keys...)
}

func (b *Builder) JSONPathQuery(column string, pathQueries ...string) *Builder {
	return addToMapField(b, &b.params.JSONPathQuery, column, pathQueries...)
}

func (b *Builder) ArrayContains(column string, values ...any) *Builder {
	return addToMapField(b, &b.params.ArrayContains, column, values...)
}

func (b *Builder) ArrayContainedBy(column string, values ...any) *Builder {
	return addToMapField(b, &b.params.ArrayContainedBy, column, values...)
}

func (b *Builder) ArrayOverlap(column string, values ...any) *Builder {
	return addToMapField(b, &b.params.ArrayOverlap, column, values...)
}

func (b *Builder) FilterGroup(group *FilterGroup) *Builder {
	if group == nil || len(group.Filters) == 0 {
		return b
	}
	b.params.FilterGroups = append(b.params.FilterGroups, group)
	return b
}

func (b *Builder) WithTokenPagination() *Builder {
	b.params.WithTokenPagination = true
	return b
}

func (b *Builder) WithoutOffset() *Builder {
	b.params.Skip = 0
	b.params.WithoutOffset = true
	return b
}

func (b *Builder) WithCountEstimate() *Builder {
	b.params.WithCountEstimate = true
	return b
}

func (b *Builder) WithoutCount() *Builder {
	b.params.WithoutCount = true
	return b
}

func (b *Builder) WithPaginationInfoOnly() *Builder {
	b.params.WithPaginationInfoOnly = true
	return b
}

func (b *Builder) CursorColumns(columns ...string) *Builder {
	b.params.CursorColumns = columns
	return b
}

func (b *Builder) addError(err error) *Builder {
	if err != nil {
		b.errs = append(b.errs, err)
	}
	return b
}

func (b *Builder) getError() error {
	if len(b.errs) > 0 {
		return errors.Join(b.errs...)
	}
	return nil
}

func (b *Builder) FromPaginationParams(params *PaginationParams) *Builder {
	if params == nil {
		return b
	}

	if params.PageToken != "" {
		b.PageToken(params.PageToken)
	}
	if params.PageSize > 0 {
		b.PageSize(params.PageSize)
	}
	if params.Skip > 0 {
		b.Skip(params.Skip)
	}

	// sorting
	for _, sort := range params.Sort {
		if sort != nil {
			b.SortByEntry(sort)
		}
	}

	// filtering
	for field, values := range params.Eq {
		b.Eq(field, values...)
	}
	for field, values := range params.NotEq {
		b.NotEq(field, values...)
	}
	for field, value := range params.Gt {
		b.Gt(field, value)
	}
	for field, value := range params.Gte {
		b.Gte(field, value)
	}
	for field, value := range params.Lt {
		b.Lt(field, value)
	}
	for field, value := range params.Lte {
		b.Lte(field, value)
	}
	for field, pair := range params.Between {
		b.Between(field, pair[0], pair[1])
	}
	for field, values := range params.In {
		b.In(field, values...)
	}
	for field, values := range params.NotIn {
		b.NotIn(field, values...)
	}
	for field, values := range params.Like {
		b.Like(field, values...)
	}
	for field, values := range params.ILike {
		b.ILike(field, values...)
	}
	for _, field := range params.IsNull {
		b.IsNull(field)
	}
	for _, field := range params.IsNotNull {
		b.IsNotNull(field)
	}
	for _, field := range params.IsEmpty {
		b.IsEmpty(field)
	}
	for _, field := range params.IsNotEmpty {
		b.IsNotEmpty(field)
	}

	// JSON filtering
	for field, values := range params.JSONContains {
		b.JSONContains(field, values...)
	}
	for field, values := range params.JSONExists {
		b.JSONExists(field, values...)
	}
	for field, values := range params.JSONAnyExists {
		b.JSONAnyExists(field, values...)
	}
	for field, values := range params.JSONAllExists {
		b.JSONAllExists(field, values...)
	}
	for field, values := range params.JSONPathQuery {
		b.JSONPathQuery(field, values...)
	}

	// array filtering
	for field, values := range params.ArrayContains {
		b.ArrayContains(field, values...)
	}
	for field, values := range params.ArrayContainedBy {
		b.ArrayContainedBy(field, values...)
	}
	for field, values := range params.ArrayOverlap {
		b.ArrayOverlap(field, values...)
	}

	b.params.filterFields = maps.Clone(params.filterFields)

	// filter groups
	for _, group := range params.FilterGroups {
		b.FilterGroup(group)
	}

	// config
	if params.WithTokenPagination {
		b.WithTokenPagination()
	}
	if params.WithoutOffset {
		b.WithoutOffset()
	}
	if params.WithCountEstimate {
		b.WithCountEstimate()
	}
	if params.WithoutCount {
		b.WithoutCount()
	}
	if params.WithPaginationInfoOnly {
		b.WithPaginationInfoOnly()
	}
	if len(params.CursorColumns) > 0 {
		b.CursorColumns(params.CursorColumns...)
	}

	return b
}

func addToMapField[T any](b *Builder, fieldMap *map[string][]T, key string, values ...T) *Builder {
	if *fieldMap == nil {
		*fieldMap = make(map[string][]T)
	}
	(*fieldMap)[key] = append((*fieldMap)[key], values...)
	return b
}

func addToSingleMapField[T any](b *Builder, fieldMap *map[string]T, key string, value T) *Builder {
	if *fieldMap == nil {
		*fieldMap = make(map[string]T)
	}
	(*fieldMap)[key] = value
	return b
}

package paginate

import (
	"database/sql"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type andWriter struct {
	builder     *strings.Builder
	firstClause bool
}

func newAndWriter(builder *strings.Builder) *andWriter {
	return &andWriter{
		builder:     builder,
		firstClause: true,
	}
}

func (aw *andWriter) Write(clause string) {
	if !aw.firstClause {
		aw.builder.WriteString(" AND ")
	}
	aw.builder.WriteString(clause)
	aw.firstClause = false
}

type QueryBuilder struct {
	params *PaginationParams
	args   []any
	argPos int
}

func NewQueryBuilder(params *PaginationParams) *QueryBuilder {
	return &QueryBuilder{
		params: params,
		args:   make([]any, 0),
		argPos: 1,
	}
}

// BuildSQL generates a SQL query
func (qb *QueryBuilder) BuildSQL() (string, []any, error) {
	if qb.params.table == "" {
		return "", nil, fmt.Errorf("table is required")
	}
	if qb.params.model == nil {
		return "", nil, fmt.Errorf("model is required")
	}
	if qb.params.PageSize <= 0 {
		return "", nil, fmt.Errorf("page size must be greater than 0")
	}

	// handle token keyset pagination with a CTE
	if qb.params.WithTokenPagination && qb.params.PageToken != "" {
		finalQuery, err := qb.buildTokenPaginationCTE()
		if err != nil {
			return "", nil, err
		}
		return finalQuery, qb.args, nil
	}

	// no existing token keyset pagination, so build a standard pagination query
	base, deferred := qb.splitAdditionalSelectColumns()

	var queryBuilder strings.Builder

	// handle SELECT columns
	if err := qb.buildSelectClause(&queryBuilder, base); err != nil {
		return "", nil, err
	}

	// handle FROM, JOIN, WHERE
	if _, err := qb.buildQueryCommon(&queryBuilder, false); err != nil {
		return "", nil, err
	}

	// handle ORDER BY clause
	orderBy, err := qb.buildOrderByClause()
	if err != nil {
		return "", nil, err
	}
	if orderBy != "" {
		queryBuilder.WriteString(" ORDER BY ")
		queryBuilder.WriteString(orderBy)
	}

	// handle OFFSET
	if !qb.params.WithoutOffset && qb.params.Skip > 0 {
		fmt.Fprintf(&queryBuilder, " OFFSET %d", qb.params.Skip)
	}

	// handle LIMIT - always fetch PageSize + 1 to detect if more data exists
	fmt.Fprintf(&queryBuilder, " LIMIT %d", qb.params.PageSize+1)

	if len(deferred) == 0 {
		return queryBuilder.String(), qb.args, nil
	}

	// Project added cols over paginated slice only
	var outer strings.Builder
	outer.WriteString("SELECT ")
	outer.WriteString(qb.params.table)
	outer.WriteString(".*, ")
	outer.WriteString(strings.Join(deferred, ", "))
	outer.WriteString(" FROM (")
	outer.WriteString(queryBuilder.String())
	outer.WriteString(") AS ")
	outer.WriteString(qb.params.table)

	return outer.String(), qb.args, nil
}

// Partition subqueries into base (each page) and deferred (post-paginate)
func (qb *QueryBuilder) splitAdditionalSelectColumns() (base, deferred []string) {
	if len(qb.params.additionalSelectColumns) == 0 {
		return nil, nil
	}

	// All base if we run joins
	if len(qb.params.joins) > 0 || qb.params.groupBy != "" {
		return qb.params.additionalSelectColumns, nil
	}

	// Sorted cols affect top level query, so add any to base
	referenced := make(map[string]bool)
	for _, sort := range qb.params.Sort {
		referenced[projectedColumn(qb.params, sort.Column)] = true
	}

	// Same with cursor columns
	for _, column := range qb.params.CursorColumns {
		referenced[projectedColumn(qb.params, column)] = true
	}

	// If resolved col name in constrained cols, add to base, else we can defer
	for _, expr := range qb.params.additionalSelectColumns {
		if referenced[extractColumnAlias(expr)] {
			base = append(base, expr)
		} else {
			deferred = append(deferred, expr)
		}
	}
	return base, deferred
}

// BuildCountSQL generates a count query
func (qb *QueryBuilder) BuildCountSQL() (string, []any, error) {
	if qb.params.table == "" {
		return "", nil, fmt.Errorf("table is required")
	}
	if qb.params.model == nil {
		return "", nil, fmt.Errorf("model is required")
	}

	grouped := qb.params.groupBy != ""

	var queryBuilder strings.Builder
	switch {
	case qb.params.WithCountEstimate && grouped:
		queryBuilder.WriteString("EXPLAIN (FORMAT JSON) SELECT 1")
	case qb.params.WithCountEstimate:
		// explain uses the planner estimate of rows that would be returned
		queryBuilder.WriteString("EXPLAIN (FORMAT JSON) SELECT *")
	case grouped:
		queryBuilder.WriteString("SELECT COUNT(*) FROM (SELECT 1")
	default:
		queryBuilder.WriteString("SELECT COUNT(*)")
	}

	// build common query parts, no need for ORDER BY, LIMIT, OFFSET
	if _, err := qb.buildQueryCommon(&queryBuilder, true); err != nil {
		return "", nil, err
	}

	if grouped {
		queryBuilder.WriteString(" ")
		queryBuilder.WriteString(qb.params.groupBy)
		if !qb.params.WithCountEstimate {
			queryBuilder.WriteString(") AS count_rows")
		}
	}

	return queryBuilder.String(), qb.args, nil
}

func (qb *QueryBuilder) buildSelectClause(queryBuilder *strings.Builder, additional []string) error {
	queryBuilder.WriteString("SELECT ")
	var columns []string
	if len(qb.params.selectColumns) > 0 {
		columns = qb.params.selectColumns
	} else if qb.params.model != nil {
		// extract columns from the model
		cols, err := GetColumnNames(qb.params.model)
		if err != nil {
			return fmt.Errorf("extracting columns from model: %w", err)
		}
		columns = cols
	}

	if len(columns) == 0 {
		return fmt.Errorf("no columns to select")
	}

	// fully qualify the column names
	for i, col := range columns {
		columns[i] = qb.qualifyColumn(col)
	}
	queryBuilder.WriteString(strings.Join(columns, ", "))

	if len(additional) > 0 {
		queryBuilder.WriteString(", ")
		queryBuilder.WriteString(strings.Join(additional, ", "))
	}
	return nil
}

func (qb *QueryBuilder) buildQueryCommon(queryBuilder *strings.Builder, skipGroupBy bool) (bool, error) {
	// FROM
	queryBuilder.WriteString(" FROM ")
	if qb.params.schema != "" {
		queryBuilder.WriteString(qb.params.schema)
		queryBuilder.WriteString(".")
	}
	queryBuilder.WriteString(qb.params.table)

	// add custom JOINs
	for _, join := range qb.params.joins {
		queryBuilder.WriteString(" ")
		queryBuilder.WriteString(join)
	}

	// WHERE clauses, only add WHERE if we have filters or where clauses
	var whereBuilder strings.Builder
	andWriter := newAndWriter(&whereBuilder)
	if err := qb.buildFilterClauses(andWriter); err != nil {
		return false, err
	}

	// Use same writer for later where-tally
	for _, clause := range qb.params.whereClauses {
		andWriter.Write(clause)
	}

	hasWhere := whereBuilder.Len() > 0 // Do we actually produce sql
	if hasWhere {
		queryBuilder.WriteString(" WHERE ")
		queryBuilder.WriteString(whereBuilder.String())
	}

	if !skipGroupBy && qb.params.groupBy != "" {
		queryBuilder.WriteString(" ")
		queryBuilder.WriteString(qb.params.groupBy)
	}

	return hasWhere, nil
}

func (qb *QueryBuilder) buildFilterClauses(andWriter *andWriter) error {
	// Build all standard filters (reused by filter groups)
	qb.buildAllFilters(andWriter, qb.params)

	// JSON and array filters
	qb.buildJSONFilters(andWriter, qb.params)
	qb.buildArrayFilters(andWriter, qb.params)

	// Filter groups (for OR logic)
	return qb.buildFilterGroups(andWriter)
}

func (qb *QueryBuilder) buildEqualityFilters(andWriter *andWriter, filters map[string][]any, operator string) {
	for column, values := range filters {
		if len(values) == 0 {
			continue
		}

		// NOTE: For IN (= ANY) and NOT IN (!= ALL), use array ops even if len is 1
		if operator == "= ANY" {
			andWriter.Write(fmt.Sprintf("%s = ANY($%d)", qb.compareColumn(column, values...), qb.addArg(values)))
			continue
		}
		if operator == "!= ALL" {
			andWriter.Write(fmt.Sprintf("%s != ALL($%d)", qb.compareColumn(column, values...), qb.addArg(values)))
			continue
		}

		if len(values) == 1 {
			andWriter.Write(fmt.Sprintf("%s %s $%d", qb.compareColumn(column, values[0]), operator, qb.addArg(values[0])))
		} else {
			switch operator {
			case "=":
				andWriter.Write(fmt.Sprintf("%s = ANY($%d)", qb.compareColumn(column, values...), qb.addArg(values)))
			case "!=":
				andWriter.Write(fmt.Sprintf("%s != ALL($%d)", qb.compareColumn(column, values...), qb.addArg(values)))
			}
		}
	}
}

func (qb *QueryBuilder) buildJSONFilters(andWriter *andWriter, params *PaginationParams) {
	// check if json object contains a value
	for column, jsonValues := range params.JSONContains {
		for _, jsonValue := range jsonValues {
			andWriter.Write(fmt.Sprintf("%s @> $%d::jsonb", qb.quoteQueryColumn(column), qb.addArg(jsonValue)))
		}
	}

	// check if json object contains a key
	for column, keys := range params.JSONExists {
		for _, key := range keys {
			andWriter.Write(fmt.Sprintf("%s ? $%d", qb.quoteQueryColumn(column), qb.addArg(key)))
		}
	}

	// check if json object contains any of the keys
	for column, keys := range params.JSONAnyExists {
		if len(keys) > 0 {
			andWriter.Write(fmt.Sprintf("%s ?| $%d", qb.quoteQueryColumn(column), qb.addArg(keys)))
		}
	}

	// check if json object contains all of the keys
	for column, keys := range params.JSONAllExists {
		if len(keys) > 0 {
			andWriter.Write(fmt.Sprintf("%s ?& $%d", qb.quoteQueryColumn(column), qb.addArg(keys)))
		}
	}

	// check if json object matches a path query
	for column, pathQueries := range params.JSONPathQuery {
		for _, pathQuery := range pathQueries {
			andWriter.Write(fmt.Sprintf("%s @? $%d", qb.quoteQueryColumn(column), qb.addArg(pathQuery)))
		}
	}
}

func (qb *QueryBuilder) buildArrayFilters(andWriter *andWriter, params *PaginationParams) {
	// check if left array contains right array
	for column, values := range params.ArrayContains {
		andWriter.Write(fmt.Sprintf("%s @> $%d", qb.quoteQueryColumn(column), qb.addArg(values)))
	}

	// check if left array is contained by right array
	for column, values := range params.ArrayContainedBy {
		andWriter.Write(fmt.Sprintf("%s <@ $%d", qb.quoteQueryColumn(column), qb.addArg(values)))
	}

	// check if arrays have any elements in common
	for column, values := range params.ArrayOverlap {
		andWriter.Write(fmt.Sprintf("%s && $%d", qb.quoteQueryColumn(column), qb.addArg(values)))
	}
}

func (qb *QueryBuilder) buildFilterGroups(andWriter *andWriter) error {
	for _, group := range qb.params.FilterGroups {
		if group == nil || len(group.Filters) == 0 {
			continue
		}

		// Build clauses directly from protobuf filters
		var groupClauses []string
		for _, filter := range group.Filters {
			clause, err := qb.buildSingleFilter(filter)
			if err != nil {
				return err
			}
			if clause != "" {
				groupClauses = append(groupClauses, clause)
			}
		}

		if len(groupClauses) == 0 {
			continue
		}

		// Join clauses with OR or AND based on the group operator
		var operator string
		switch group.Operator {
		case queryPB.LogicalOperator_LOGICAL_OPERATOR_OR:
			operator = " OR "
		case queryPB.LogicalOperator_LOGICAL_OPERATOR_AND, queryPB.LogicalOperator_LOGICAL_OPERATOR_UNSPECIFIED:
			operator = " AND "
		default:
			operator = " AND "
		}

		groupClause := "(" + strings.Join(groupClauses, operator) + ")"
		andWriter.Write(groupClause)
	}
	return nil
}

// buildSingleFilter shares value conversion and SQL semantics with ordinary filters.
func (qb *QueryBuilder) buildSingleFilter(filter *queryPB.Filter) (string, error) {
	if filter == nil {
		return "", nil
	}
	params := NewPaginationParams()
	params.filterFields = maps.Clone(qb.params.filterFields)
	if err := convertFilters([]*queryPB.Filter{filter}, params, nil, qb.isBooleanColumn(filter.Column)); err != nil {
		return "", err
	}
	var clause strings.Builder
	writer := newAndWriter(&clause)
	qb.buildAllFilters(writer, params)
	qb.buildJSONFilters(writer, params)
	qb.buildArrayFilters(writer, params)
	return "(" + clause.String() + ")", nil
}

// Grouped filters are converted after protobuf field names have been mapped to
// SQL columns, so use the database model's type to recognize false values.
func (qb *QueryBuilder) isBooleanColumn(column string) bool {
	var columnType reflect.Type
	if value, ok := qb.params.additionalColumnTypes[projectedColumn(qb.params, column)]; ok {
		columnType = reflect.TypeOf(value)
	} else {
		model, err := indirectModelValue(qb.params.model)
		if err != nil || model.Kind() != reflect.Struct {
			return false
		}
		if column == qb.qualifyColumn(unqualifyColumn(column)) || column == qb.params.table+"."+unqualifyColumn(column) {
			column = unqualifyColumn(column)
		}
		index, ok := GetFieldIndex(model.Type(), column)
		if !ok {
			return false
		}
		columnType = model.Type().Field(index).Type
	}
	if columnType == nil {
		return false
	}
	for columnType.Kind() == reflect.Pointer || columnType.Kind() == reflect.Slice || columnType.Kind() == reflect.Array {
		columnType = columnType.Elem()
	}
	return columnType.Kind() == reflect.Bool || columnType == reflect.TypeFor[pgtype.Bool]() || columnType == reflect.TypeFor[sql.NullBool]()
}

// buildAllFilters builds all filter clauses for a given params (reused for filter groups)
func (qb *QueryBuilder) buildAllFilters(andWriter *andWriter, params *PaginationParams) {
	qb.buildEqualityFilters(andWriter, params.Eq, "=")
	qb.buildEqualityFilters(andWriter, params.NotEq, "!=")
	qb.buildEqualityFilters(andWriter, params.In, "= ANY")
	qb.buildEqualityFilters(andWriter, params.NotIn, "!= ALL")

	for column, value := range params.Gt {
		andWriter.Write(fmt.Sprintf("%s > $%d", qb.compareColumn(column, value), qb.addArg(value)))
	}
	for column, value := range params.Gte {
		andWriter.Write(fmt.Sprintf("%s >= $%d", qb.compareColumn(column, value), qb.addArg(value)))
	}
	for column, value := range params.Lt {
		andWriter.Write(fmt.Sprintf("%s < $%d", qb.compareColumn(column, value), qb.addArg(value)))
	}
	for column, value := range params.Lte {
		andWriter.Write(fmt.Sprintf("%s <= $%d", qb.compareColumn(column, value), qb.addArg(value)))
	}
	for column, values := range params.Between {
		andWriter.Write(fmt.Sprintf("%s BETWEEN $%d AND $%d",
			qb.compareColumn(column, values[0], values[1]), qb.addArg(values[0]), qb.addArg(values[1])))
	}
	for column, patterns := range params.Like {
		for _, pattern := range patterns {
			p := pattern
			if !strings.Contains(p, "%") && !strings.Contains(p, "_") {
				p = "%" + p + "%"
			}
			if qb.isUUIDColumn(column) {
				andWriter.Write(fmt.Sprintf("%s::text LIKE $%d", qb.quoteQueryColumn(column), qb.addArg(p)))
			} else {
				andWriter.Write(fmt.Sprintf("%s LIKE $%d", qb.quoteQueryColumn(column), qb.addArg(p)))
			}
		}
	}
	for column, patterns := range params.ILike {
		for _, pattern := range patterns {
			p := pattern
			if !strings.Contains(p, "%") && !strings.Contains(p, "_") {
				p = "%" + p + "%"
			}
			if qb.isUUIDColumn(column) {
				andWriter.Write(fmt.Sprintf("%s::text ILIKE $%d", qb.quoteQueryColumn(column), qb.addArg(p)))
			} else {
				andWriter.Write(fmt.Sprintf("%s ILIKE $%d", qb.quoteQueryColumn(column), qb.addArg(p)))
			}
		}
	}
	for _, column := range params.IsNull {
		andWriter.Write(fmt.Sprintf("%s IS NULL", qb.quoteQueryColumn(column)))
	}
	for _, column := range params.IsNotNull {
		andWriter.Write(fmt.Sprintf("%s IS NOT NULL", qb.quoteQueryColumn(column)))
	}
	for _, column := range params.IsEmpty {
		quotedCol := qb.quoteQueryColumn(column)
		if qb.isUUIDColumn(column) {
			andWriter.Write(fmt.Sprintf("%s IS NULL", quotedCol))
		} else {
			andWriter.Write(fmt.Sprintf("(%s IS NULL OR %s::text = '' OR %s::text = '0' OR %s::text = '{}')", quotedCol, quotedCol, quotedCol, quotedCol))
		}
	}
	for _, column := range params.IsNotEmpty {
		quotedCol := qb.quoteQueryColumn(column)
		if qb.isUUIDColumn(column) {
			andWriter.Write(fmt.Sprintf("%s IS NOT NULL", quotedCol))
		} else {
			andWriter.Write(fmt.Sprintf("(%s IS NOT NULL AND %s::text != '' AND %s::text != '0' AND %s::text != '{}')", quotedCol, quotedCol, quotedCol, quotedCol))
		}
	}
}

func (qb *QueryBuilder) buildOrderByClause() (string, error) {
	inner, _, err := qb.buildOrderByClauses()
	return inner, err
}

// buildOrderByClauses renders the ORDER BY clause twice in a single pass,
// once qualified for the base table (usable inside the query/CTE where the
// table and its joins are in scope) and once against the output column names
// of the candidates CTE (where only the projected column names are visible).
// Rendering both in one pass keeps a single set of placeholder args shared by
// the two clauses.
func (qb *QueryBuilder) buildOrderByClauses() (inner, outer string, err error) {
	cursorColumns, err := resolveCursorColumns(qb.params)
	if err != nil {
		return "", "", err
	}

	if len(qb.params.Sort) == 0 {
		innerOrders := make([]string, 0, len(cursorColumns))
		outerOrders := make([]string, 0, len(cursorColumns))
		for _, column := range cursorColumns {
			innerOrders = append(innerOrders, fmt.Sprintf("%s ASC", qb.quoteColumn(qb.qualifyColumn(column))))
			outerOrders = append(outerOrders, fmt.Sprintf("%s ASC", qb.quoteColumn(projectedColumn(qb.params, column))))
		}
		return strings.Join(innerOrders, ", "), strings.Join(outerOrders, ", "), nil
	}

	var innerOrders, outerOrders []string
	orderedColumns := make([]string, 0, len(qb.params.Sort))

	for _, sort := range qb.params.Sort {
		column := qb.quoteQueryColumn(sort.Column)
		outerColumn := qb.quoteColumn(projectedColumn(qb.params, sort.Column))
		orderedColumns = append(orderedColumns, sort.Column)
		direction := "ASC"
		if sort.Order == queryPB.SortOrder_SORT_ORDER_DESC {
			direction = "DESC"
		}

		var innerClause, outerClause string

		switch {
		case sort.JSONPath != "":
			// get json path as text (data #>> '{user,profile,name}'  -- Returns "John" as TEXT)
			innerClause = fmt.Sprintf("%s #>> '{%s}' %s", column, strings.ReplaceAll(sort.JSONPath, ".", ","), direction)
			outerClause = fmt.Sprintf("%s #>> '{%s}' %s", outerColumn, strings.ReplaceAll(sort.JSONPath, ".", ","), direction)
		case sort.JSONText != "":
			// get json field as test (data ->> 'name'  -- Returns "John" as TEXT)
			innerClause = fmt.Sprintf("%s ->> '%s' %s", column, sort.JSONText, direction)
			outerClause = fmt.Sprintf("%s ->> '%s' %s", outerColumn, sort.JSONText, direction)
		case sort.JSONNumeric != "":
			// get json field as numeric (data ->> 'age'  -- Returns 25 as NUMERIC)
			innerClause = fmt.Sprintf("CAST(%s ->> '%s' AS NUMERIC) %s", column, sort.JSONNumeric, direction)
			outerClause = fmt.Sprintf("CAST(%s ->> '%s' AS NUMERIC) %s", outerColumn, sort.JSONNumeric, direction)
		case sort.ArrayIndex >= 1:
			// postgres array index is 1-based (0 means not set)
			argPos := qb.addArg(sort.ArrayIndex)
			innerClause = fmt.Sprintf("%s[$%d] %s", column, argPos, direction)
			outerClause = fmt.Sprintf("%s[$%d] %s", outerColumn, argPos, direction)
		case sort.ArrayLength:
			// sort by cardinality of array
			innerClause = fmt.Sprintf("ARRAY_LENGTH(%s, 1) %s", column, direction)
			outerClause = fmt.Sprintf("ARRAY_LENGTH(%s, 1) %s", outerColumn, direction)
		case sort.ArrayContains != nil:
			// prioritize arrays that contain a specific value
			argPos := qb.addArg(sort.ArrayContains)
			innerClause = fmt.Sprintf("ARRAY_POSITION(%s, $%d) %s", column, argPos, direction)
			outerClause = fmt.Sprintf("ARRAY_POSITION(%s, $%d) %s", outerColumn, argPos, direction)
		default:
			innerClause = fmt.Sprintf("%s %s", column, direction)
			outerClause = fmt.Sprintf("%s %s", outerColumn, direction)
		}

		innerOrders = append(innerOrders, innerClause)
		outerOrders = append(outerOrders, outerClause)
	}

	for _, cursorColumn := range cursorColumns {
		if slices.Contains(orderedColumns, cursorColumn) {
			continue
		}
		direction := qb.cursorColumnDirection(cursorColumn)
		innerOrders = append(innerOrders, fmt.Sprintf("%s %s", qb.quoteColumn(qb.qualifyColumn(cursorColumn)), direction))
		outerOrders = append(outerOrders, fmt.Sprintf("%s %s", qb.quoteColumn(projectedColumn(qb.params, cursorColumn)), direction))
	}

	return strings.Join(innerOrders, ", "), strings.Join(outerOrders, ", "), nil
}

// unqualifyColumn strips any schema/table qualifier from a column reference,
// yielding the name under which the column appears in a result set.
func unqualifyColumn(column string) string {
	if _, after, ok := strings.CutLast(column, "."); ok {
		return after
	}
	return column
}

func (qb *QueryBuilder) cursorColumnDirection(column string) string {
	for _, sort := range qb.params.Sort {
		if sort.Column == column {
			if sort.Order == queryPB.SortOrder_SORT_ORDER_DESC {
				return "DESC"
			}
			return "ASC"
		}
	}

	if len(qb.params.Sort) > 0 && qb.params.Sort[0].Order == queryPB.SortOrder_SORT_ORDER_DESC {
		return "DESC"
	}

	return "ASC"
}

func (qb *QueryBuilder) qualifyColumn(column string) string {
	// if column is already qualified, return as-is
	if strings.Contains(column, ".") {
		return column
	}

	// build fully qualified name: schema.table.column or table.column
	if qb.params.schema != "" {
		return fmt.Sprintf("%s.%s.%s", qb.params.schema, qb.params.table, column)
	}
	return fmt.Sprintf("%s.%s", qb.params.table, column)
}

// quoteQueryColumn qualifies base-model columns when joins could make them
// ambiguous. Explicit table aliases and additional projected columns are kept.
func (qb *QueryBuilder) quoteQueryColumn(column string) string {
	if len(qb.params.joins) == 0 || strings.Contains(column, ".") {
		return qb.quoteColumn(column)
	}
	info, err := GetTypeInfo(qb.params.model)
	if err != nil {
		return qb.quoteColumn(column)
	}
	if _, ok := info.FieldMappings[column]; !ok {
		return qb.quoteColumn(column)
	}
	return qb.quoteColumn(qb.qualifyColumn(column))
}

func (qb *QueryBuilder) quoteColumn(column string) string {
	// handle schema.table format
	parts := strings.Split(column, ".")
	var quotedParts []string

	for _, part := range parts {
		// quote each part separately, escaping any existing quotes
		quotedPart := strings.ReplaceAll(part, "\"", "\"\"")
		quotedParts = append(quotedParts, fmt.Sprintf("\"%s\"", quotedPart))
	}

	return strings.Join(quotedParts, ".")
}

func (qb *QueryBuilder) addArg(arg any) int {
	pos := qb.argPos
	qb.args = append(qb.args, arg)
	qb.argPos++
	return pos
}

// Checks if every string value can be cast to a uuid
func uuidComparable(values ...any) bool {
	for _, value := range values {
		if s, ok := value.(string); ok {
			if uuid.Validate(s) != nil {
				return false
			}
		}
	}
	return true
}

// Check if col can be compared as native type for current val, else text cast
func (qb *QueryBuilder) compareColumn(column string, values ...any) string {
	quoted := qb.quoteQueryColumn(column)
	if qb.isUUIDColumn(column) && !uuidComparable(values...) {
		quoted += "::text"
	}
	return quoted
}

// isUUIDColumn checks if a column is a UUID type
func (qb *QueryBuilder) isUUIDColumn(column string) bool {
	// Join cols are not on the model
	if colType, ok := qb.params.additionalColumnTypes[column]; ok {
		return isUUIDType(reflect.TypeOf(colType))
	}

	if qb.params.model == nil {
		return false
	}

	typeInfo, err := GetTypeInfo(qb.params.model)
	if err != nil {
		return false
	}

	baseColumn := unqualifyColumn(column)
	if column == qb.qualifyColumn(baseColumn) || column == qb.params.table+"."+baseColumn {
		column = baseColumn
	}
	if colType, ok := qb.params.additionalColumnTypes[projectedColumn(qb.params, column)]; ok {
		return isUUIDType(reflect.TypeOf(colType))
	}
	return typeInfo.UUIDColumns[column]
}

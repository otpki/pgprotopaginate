package paginate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/samber/oops"
)

// GenerateNextTokenFromModel creates a token for the next page from a model struct
func GenerateNextTokenFromModel[T any](model T, params *PaginationParams) (string, error) {
	if !params.WithTokenPagination {
		return "", nil
	}

	cursorModel := any(model)
	if _, ok := cursorModel.(map[string]any); ok {
		cursorModel = params.model
	}
	cursorColumns, err := resolveCursorColumnsForModel(params, cursorModel)
	if err != nil {
		return "", err
	}

	var values map[string]any
	if row, ok := any(model).(map[string]any); ok {
		values = row
	} else {
		columns, err := GetColumnNames(model)
		if err != nil {
			return "", err
		}
		values, err = ExtractModelValues(model, columns)
		if err != nil {
			return "", oops.In(ServiceName).Wrapf(err, "extracting cursor values from model")
		}
	}
	cursorValues := make(map[string]any, len(cursorColumns))
	for _, column := range cursorColumns {
		name := projectedColumn(params, column)
		value, ok := values[name]
		if !ok {
			return "", oops.In(ServiceName).Errorf("cursor column %s not found in row", name)
		}
		cursorValues[column] = value
	}
	return generateToken(params, cursorValues, cursorColumns)
}

func resolveCursorColumns(params *PaginationParams) ([]string, error) {
	if params == nil {
		return nil, oops.In(ServiceName).New("pagination params cannot be nil")
	}

	return resolveCursorColumnsForModel(params, params.model)
}

func resolveCursorColumnsForModel(params *PaginationParams, model any) ([]string, error) {
	if params == nil {
		return nil, oops.In(ServiceName).New("pagination params cannot be nil")
	}

	if len(params.CursorColumns) > 0 {
		return params.CursorColumns, nil
	}

	var cursorColumns []string
	for _, sort := range params.Sort {
		if sort != nil && sort.Column != "" {
			cursorColumns = append(cursorColumns, sort.Column)
		}
	}
	if len(cursorColumns) > 0 {
		return appendDefaultTieBreaker(model, cursorColumns)
	}

	return deriveDefaultCursorColumns(model)
}

func appendDefaultTieBreaker(model any, cursorColumns []string) ([]string, error) {
	tieBreaker, err := defaultTieBreakerColumn(model)
	if err != nil {
		return nil, err
	}
	if tieBreaker == "" {
		return cursorColumns, nil
	}
	for _, column := range cursorColumns {
		if unqualifyColumn(column) == tieBreaker {
			return cursorColumns, nil
		}
	}

	return append(cursorColumns, tieBreaker), nil
}

func deriveDefaultCursorColumns(model any) ([]string, error) {
	tieBreaker, err := defaultTieBreakerColumn(model)
	if err != nil {
		return nil, err
	}
	if tieBreaker != "" {
		return []string{tieBreaker}, nil
	}

	columns, err := GetColumnNames(model)
	if err != nil {
		return nil, oops.In(ServiceName).Wrapf(err, "deriving default cursor column")
	}
	if len(columns) == 0 {
		return nil, oops.In(ServiceName).New("no cursor columns specified")
	}

	return []string{columns[0]}, nil
}

func defaultTieBreakerColumn(model any) (string, error) {
	columns, err := GetColumnNames(model)
	if err != nil {
		return "", oops.In(ServiceName).Wrapf(err, "deriving default cursor column")
	}

	if slices.Contains(columns, "id") {
		return "id", nil
	}

	return "", nil
}

func (qb *QueryBuilder) buildTokenPaginationCTE() (string, error) {
	// decode and validate the token
	if qb.params.PageToken == "" {
		return "", oops.In(ServiceName).New("page token is required for token pagination")
	}

	token, err := decodeToken(qb.params.PageToken)
	if err != nil {
		return "", oops.In(ServiceName).Wrapf(err, "decoding page token")
	}

	if err := validateToken(qb.params.PageToken, qb.params); err != nil {
		return "", err
	}

	// extract the cursor values and sort columns from token
	cursorValues := token.CursorValues
	sortColumns := token.SortColumns
	if cursorValues == nil || len(sortColumns) == 0 {
		return "", oops.In(ServiceName).New("invalid token: missing cursor information")
	}

	// if explicit cursor columns are configured, ensure they match the token
	if len(qb.params.CursorColumns) > 0 {
		// validate that the token's sort columns match our configured cursor columns
		if len(qb.params.CursorColumns) != len(sortColumns) {
			return "", oops.In(ServiceName).New("token cursor columns do not match configured cursor columns")
		}
		for i, col := range qb.params.CursorColumns {
			if i >= len(sortColumns) || sortColumns[i] != col {
				return "", oops.In(ServiceName).Errorf("token cursor column %d does not match configured cursor column", i)
			}
		}
		// use configured cursor columns for consistency
		sortColumns = qb.params.CursorColumns
	}

	// build cursor condition
	cursorCondition := qb.buildCursorCondition(cursorValues, sortColumns)

	// build the CTE
	base, deferred := qb.splitAdditionalSelectColumns()

	var queryBuilder strings.Builder
	queryBuilder.WriteString("WITH candidates AS (\n  ")

	// handle SELECT columns
	if err := qb.buildSelectClause(&queryBuilder, base); err != nil {
		return "", err
	}

	// build FROM, JOIN, and WHERE
	hasWhere, err := qb.buildQueryCommon(&queryBuilder, true)
	if err != nil {
		return "", err
	}

	// add cursor condition
	if hasWhere {
		queryBuilder.WriteString(" AND (")
		queryBuilder.WriteString(cursorCondition)
		queryBuilder.WriteString(")")
	} else {
		queryBuilder.WriteString(" WHERE (")
		queryBuilder.WriteString(cursorCondition)
		queryBuilder.WriteString(")")
	}

	if qb.params.groupBy != "" {
		queryBuilder.WriteString(" ")
		queryBuilder.WriteString(qb.params.groupBy)
	}

	// add ORDER BY for CTE. the outer select over candidates needs the same
	// ordering against the projected column names, otherwise the row order
	// (and therefore the row the next page token is minted from) is undefined
	innerOrderBy, outerOrderBy, err := qb.buildOrderByClauses()
	if err != nil {
		return "", err
	}
	if innerOrderBy != "" {
		queryBuilder.WriteString(" ORDER BY ")
		queryBuilder.WriteString(innerOrderBy)
	}

	// add LIMIT (fetch page_size + skip + 1 to account for skip and overflow detection)
	limit := qb.params.PageSize + qb.params.Skip + 1
	fmt.Fprintf(&queryBuilder, " LIMIT %d", limit)

	// build the actual query
	if len(deferred) > 0 {
		queryBuilder.WriteString("\n)\nSELECT ")
		queryBuilder.WriteString(qb.params.table)
		queryBuilder.WriteString(".*, ")
		queryBuilder.WriteString(strings.Join(deferred, ", "))
		queryBuilder.WriteString(" FROM candidates AS ")
		queryBuilder.WriteString(qb.params.table)
	} else {
		queryBuilder.WriteString("\n)\nSELECT * FROM candidates")
	}
	if outerOrderBy != "" {
		queryBuilder.WriteString(" ORDER BY ")
		queryBuilder.WriteString(outerOrderBy)
	}

	// OFFSET
	if !qb.params.WithoutOffset && qb.params.Skip > 0 {
		fmt.Fprintf(&queryBuilder, " OFFSET %d", qb.params.Skip)
	}

	// final (evaluated) limit (page_size + 1 to detect if more pages exist)
	fmt.Fprintf(&queryBuilder, " LIMIT %d", qb.params.PageSize+1)

	return queryBuilder.String(), nil
}

func (qb *QueryBuilder) buildCursorCondition(cursorValues map[string]any, sortColumns []string) string {
	if len(sortColumns) == 0 {
		return "true"
	}

	// build the cursor condition based on sort order
	var conditions []string
	var rowValueColumns []string
	var rowValuePlaceholders []string

	for i, column := range sortColumns {
		// qualify cursor columns with the base table (same as the ORDER BY
		// tie-breaker columns) so the predicate stays unambiguous when the
		// query joins other tables that carry the same column name
		quotedColumn := qb.quoteColumn(qb.qualifyColumn(column))
		rowValueColumns = append(rowValueColumns, quotedColumn)

		// get the cursor value for this column and add it to args once
		cursorValue := cursorValues[column]
		paramIndex := qb.addArg(cursorValue)
		rowValuePlaceholders = append(rowValuePlaceholders, fmt.Sprintf("$%d", paramIndex))

		sortDirection := qb.cursorColumnDirection(column)

		// Build condition for this column based on sort direction
		if i == 0 {
			// for the first column, use direct comparison
			if sortDirection == "ASC" {
				conditions = append(conditions, fmt.Sprintf("%s > $%d", quotedColumn, paramIndex))
			} else {
				conditions = append(conditions, fmt.Sprintf("%s < $%d", quotedColumn, paramIndex))
			}
		} else {
			// for subsequent columns, we need to handle equality of previous columns
			prevColumns := strings.Join(rowValueColumns[:i], ", ")
			prevValues := strings.Join(rowValuePlaceholders[:i], ", ")

			if sortDirection == "ASC" {
				conditions = append(conditions, fmt.Sprintf("(%s) = (%s) AND %s > $%d",
					prevColumns, prevValues, quotedColumn, paramIndex))
			} else {
				conditions = append(conditions, fmt.Sprintf("(%s) = (%s) AND %s < $%d",
					prevColumns, prevValues, quotedColumn, paramIndex))
			}
		}
	}

	// combine conditions with OR
	if len(conditions) == 1 {
		return conditions[0]
	}

	// use row value comparison for postgres 18 efficiency
	if len(rowValueColumns) > 1 && qb.cursorColumnsSameDirection(sortColumns) {
		// determine overall sort direction
		firstSortDirection := qb.cursorColumnDirection(sortColumns[0])

		operator := ">"
		if firstSortDirection == "DESC" {
			operator = "<"
		}

		return fmt.Sprintf("(%s) %s (%s)",
			strings.Join(rowValueColumns, ", "),
			operator,
			strings.Join(rowValuePlaceholders, ", "))
	}

	return strings.Join(conditions, " OR ")
}

func (qb *QueryBuilder) cursorColumnsSameDirection(columns []string) bool {
	if len(columns) < 2 {
		return true
	}

	firstDirection := qb.cursorColumnDirection(columns[0])
	for _, column := range columns[1:] {
		if qb.cursorColumnDirection(column) != firstDirection {
			return false
		}
	}

	return true
}

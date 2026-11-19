package paginate

import (
	"encoding/json"
	"reflect"
	"strings"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/jackc/pgx/v5"
	"github.com/samber/oops"
)

// CreatePaginationResponse creates a PaginationResponse from an array of model values and count query result
func CreatePaginationResponse[T any](models []T, countRows pgx.Rows, params *PaginationParams) ([]T, *queryPB.PaginationResponse, error) {
	// parse count based on whether it's an estimate or exact count
	response := &queryPB.PaginationResponse{
		PageSize: params.PageSize,
		Skip:     params.Skip,
	}

	if countRows != nil {
		var totalCount int64
		if params.WithCountEstimate {
			// no need to close countRows here, it will be closed in ParseCountEstimate
			estimate, err := ParseCountEstimate(countRows)
			if err != nil {
				return nil, nil, oops.In(ServiceName).Wrapf(err, "parsing count estimate")
			}
			totalCount = estimate
		} else {
			defer countRows.Close()
			// Parse exact COUNT(*) result
			if !countRows.Next() {
				return nil, nil, oops.In(ServiceName).New("no rows returned from count query")
			}
			if err := countRows.Scan(&totalCount); err != nil {
				return nil, nil, oops.In(ServiceName).Wrapf(err, "scanning count result")
			}
		}
		response.TotalCount = totalCount
	}

	// determine if there are more records
	// we always fetch PageSize + 1 records, so if we got more than PageSize, there are more records
	if params.PageSize > 0 {
		response.HasMore = int64(len(models)) > params.PageSize
		// only use total count comparison if we have an exact count (not estimate)
		if !params.WithCountEstimate && response.TotalCount > 0 {
			response.HasMore = response.HasMore && (params.Skip+params.PageSize) < response.TotalCount
		}
	} else {
		response.HasMore = false
	}

	// trim the extra record if we fetched more than PageSize, return trimmed for reassignment
	if len(models) > int(params.PageSize) {
		models = models[:params.PageSize]
	}

	// generate next page token if needed
	if response.HasMore && params.WithTokenPagination && len(models) > 0 {
		// use the last model to generate the next token
		lastModel := models[len(models)-1]
		if nextToken, err := GenerateNextTokenFromModel(lastModel, params); err == nil && nextToken != "" {
			response.NextPageToken = nextToken
		}
	}

	return models, response, nil
}

// ParseCountEstimate parses the result from an EXPLAIN (FORMAT JSON) query and returns the estimated row count
func ParseCountEstimate(rows pgx.Rows) (int64, error) {
	defer rows.Close()

	if !rows.Next() {
		return 0, oops.In(ServiceName).New("no rows returned from EXPLAIN query")
	}

	var explainJSON []byte
	if err := rows.Scan(&explainJSON); err != nil {
		return 0, oops.In(ServiceName).Wrapf(err, "scanning EXPLAIN result")
	}

	// parse the JSON structure
	var explainResult []map[string]any
	if err := json.Unmarshal(explainJSON, &explainResult); err != nil {
		return 0, oops.In(ServiceName).Wrapf(err, "parsing EXPLAIN JSON")
	}

	if len(explainResult) == 0 {
		return 0, oops.In(ServiceName).New("empty EXPLAIN result")
	}

	// extract plan rows from the first plan
	plan, ok := explainResult[0]["Plan"].(map[string]any)
	if !ok {
		return 0, oops.In(ServiceName).New("Plan not found in EXPLAIN result")
	}

	// JSON unmarshaler converts all JSON numbers to float64 by default
	planRows, ok := plan["Plan Rows"].(float64)
	if !ok {
		return 0, oops.In(ServiceName).New("Plan Rows not found or not numeric in EXPLAIN result")
	}

	return int64(planRows), nil
}

// ScanRows scans pgx rows into a slice of model values of type T
func ScanRows[T any](rows pgx.Rows) ([]T, error) {
	defer rows.Close()

	var results []T

	// get column names from the result set
	columnNames := rows.FieldDescriptions()
	if len(columnNames) == 0 {
		// field descriptions are nil when the server rejected the query - drain
		// the result reader to surface the real error instead of a generic message
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, oops.In(ServiceName).Wrapf(err, "executing query")
		}
		return nil, oops.In(ServiceName).New("no columns in result set")
	}

	// extract column names as strings
	var columns []string
	for _, col := range columnNames {
		columns = append(columns, col.Name)
	}

	// create a new model instance for each row
	for rows.Next() {
		// create a zero value of type T and get a pointer to it
		var newModel T
		modelPtr := &newModel

		// prepare values for scanning
		values, err := prepareScanValues(modelPtr, columns)
		if err != nil {
			return nil, oops.In(ServiceName).Wrapf(err, "preparing scan values")
		}

		// scan the row
		if err := rows.Scan(values...); err != nil {
			return nil, oops.In(ServiceName).Wrapf(err, "scanning row")
		}

		results = append(results, newModel)
	}

	if err := rows.Err(); err != nil {
		return nil, oops.In(ServiceName).Wrapf(err, "iterating rows")
	}

	return results, nil
}

func prepareScanValues[T any](model *T, columns []string) ([]any, error) {
	v := reflect.ValueOf(model)
	if v.Kind() != reflect.Pointer {
		return nil, oops.In(ServiceName).New("model must be a pointer")
	}

	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return nil, oops.In(ServiceName).New("model must point to a struct")
	}

	var values []any

	for _, column := range columns {
		// try to find the field for this column using cached type info
		typeInfo, err := GetTypeInfo(model)
		if err != nil {
			return nil, oops.In(ServiceName).Wrapf(err, "getting type info")
		}

		fieldIndex, found := typeInfo.FieldMappings[column]
		if !found {
			return nil, oops.In(ServiceName).Errorf("column %s not found in model", column)
		}

		// get a pointer to the field for scanning, for example:
		// values = []any{
		// 	&(user.ID),    // Pointer to ID field
		// 	&(user.Name),  // Pointer to Name field
		// }
		fieldValue := v.Field(fieldIndex)
		if !fieldValue.CanAddr() {
			return nil, oops.In(ServiceName).Errorf("field %s is not addressable", column)
		}

		values = append(values, fieldValue.Addr().Interface())
	}

	return values, nil
}

// ScanRowsWithAdditional scans pgx rows into a slice of model values with additional data
func ScanRowsWithAdditional[T any](rows pgx.Rows, additionalColumns []string, additionalColumnTypes map[string]any, additionalDataHandler func(map[string]any) error) ([]T, error) {
	defer rows.Close()

	var results []T

	// get column names from the result set
	columnNames := rows.FieldDescriptions()
	if len(columnNames) == 0 {
		// field descriptions are nil when the server rejected the query - drain
		// the result reader to surface the real error instead of a generic message
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, oops.In(ServiceName).Wrapf(err, "executing query")
		}
		return nil, oops.In(ServiceName).New("no columns in result set")
	}

	// extract column names as strings
	var columns []string
	for _, col := range columnNames {
		columns = append(columns, col.Name)
	}

	// create a new model instance for each row
	for rows.Next() {
		// create a zero value of type T and get a pointer to it
		var newModel T
		modelPtr := &newModel

		// Separate model columns from additional columns
		modelColumns, additionalCols := separateColumns(columns, additionalColumns)

		// Prepare scan values for model and additional data
		values, additionalValues, err := prepareMixedScanValues(modelPtr, modelColumns, additionalCols, additionalColumnTypes)
		if err != nil {
			return nil, oops.In(ServiceName).Wrapf(err, "preparing mixed scan values")
		}

		// Combine all values for scanning
		values = append(values, additionalValues...)

		// scan the row
		if err := rows.Scan(values...); err != nil {
			return nil, oops.In(ServiceName).Wrapf(err, "scanning row")
		}

		// Handle additional data if handler provided
		if additionalDataHandler != nil {
			additionalDataMap := make(map[string]any)
			for i, col := range additionalCols {
				additionalDataMap[col] = additionalScanValue(additionalValues[i])
			}
			if err := additionalDataHandler(additionalDataMap); err != nil {
				return nil, oops.In(ServiceName).Wrapf(err, "handling additional row data")
			}
		}

		results = append(results, newModel)
	}

	if err := rows.Err(); err != nil {
		return nil, oops.In(ServiceName).Wrapf(err, "iterating rows")
	}

	return results, nil
}

// separateColumns separates model columns from additional columns
func separateColumns(allColumns, additionalColumns []string) ([]string, []string) {
	additionalSet := make(map[string]bool)
	for _, col := range additionalColumns {
		// Extract alias from SQL expression if it exists
		alias := extractColumnAlias(col)
		additionalSet[alias] = true
	}

	var modelCols []string
	var addCols []string
	for _, col := range allColumns {
		if additionalSet[col] {
			addCols = append(addCols, col)
		} else {
			modelCols = append(modelCols, col)
		}
	}
	return modelCols, addCols
}

// extractColumnAlias extracts the column alias from a SQL expression
// Examples:
// "column_name" -> "column_name"
// "expression AS alias" -> "alias"
// "expression alias" -> "alias"
func extractColumnAlias(sqlExpr string) string {
	// Convert to uppercase for case-insensitive matching
	upperExpr := strings.ToUpper(sqlExpr)

	// Look for "AS alias" pattern
	asIndex := strings.LastIndex(upperExpr, " AS ")
	if asIndex != -1 {
		// Extract everything after " AS "
		alias := sqlExpr[asIndex+4:]
		// Remove any surrounding quotes
		alias = strings.Trim(alias, `"'`)
		return strings.TrimSpace(alias)
	}

	// Look for space-separated alias (no AS keyword)
	lastSpace := strings.LastIndex(sqlExpr, " ")
	if lastSpace != -1 && lastSpace < len(sqlExpr)-1 {
		// Check if this looks like an alias (not a function call, etc.)
		afterSpace := sqlExpr[lastSpace+1:]
		if !strings.Contains(afterSpace, "(") && !strings.Contains(afterSpace, ",") {
			alias := strings.TrimSpace(afterSpace)
			// Remove any surrounding quotes
			alias = strings.Trim(alias, `"'`)
			return alias
		}
	}

	// If no alias found, assume the whole expression is the column name
	return strings.TrimSpace(strings.Trim(sqlExpr, `"'`))
}

// prepareMixedScanValues prepares scan values for both model and additional columns
func prepareMixedScanValues[T any](model *T, modelColumns, additionalColumns []string, additionalColumnTypes map[string]any) ([]any, []any, error) {
	// Get model scan values
	modelValues, err := prepareScanValues(model, modelColumns)
	if err != nil {
		return nil, nil, err
	}

	// Prepare additional column scan values
	var additionalValues []any
	for _, column := range additionalColumns {
		additionalValues = append(additionalValues, prepareAdditionalScanValue(column, additionalColumnTypes))
	}

	return modelValues, additionalValues, nil
}

func prepareAdditionalScanValue(column string, additionalColumnTypes map[string]any) any {
	if additionalColumnTypes == nil {
		var val any
		return &val
	}

	typeHint, ok := additionalColumnTypes[column]
	if !ok || typeHint == nil {
		var val any
		return &val
	}

	hintType := reflect.TypeOf(typeHint)
	if hintType.Kind() == reflect.Pointer {
		hintType = hintType.Elem()
	}
	return reflect.New(hintType).Interface()
}

func additionalScanValue(scanTarget any) any {
	v := reflect.ValueOf(scanTarget)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return scanTarget
	}
	return v.Elem().Interface()
}

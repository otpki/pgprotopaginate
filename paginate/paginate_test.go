package paginate

import (
	"slices"
	"strings"
	"testing"
	"time"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestModel represents a simple test model for pagination
type TestModel struct {
	ID        int64          `db:"id"`
	Name      string         `db:"name"`
	Email     string         `db:"email"`
	Age       int            `db:"age"`
	Active    bool           `db:"active"`
	CreatedAt time.Time      `db:"created_at"`
	Tags      []string       `db:"tags"`
	Metadata  map[string]any `db:"metadata"`
}

type CursorFallbackModel struct {
	Name string `db:"name"`
	Code string `db:"code"`
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newTestPool(t)

	// create test table
	_, err := pool.Exec(t.Context(), `
		DROP TABLE IF EXISTS test_models;
		CREATE TABLE test_models (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255) NOT NULL,
			age INTEGER,
			active BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT NOW(),
			tags TEXT[],
			metadata JSONB
		);
	`)
	require.NoError(t, err)

	// insert test data
	_, err = pool.Exec(t.Context(), `
		INSERT INTO test_models (name, email, age, active, created_at, tags, metadata) VALUES
		('Alice', 'alice@example.com', 25, true, '2023-01-01 10:00:00', ARRAY['user', 'admin'], '{"role": "admin", "level": 5}'),
		('Bob', 'bob@example.com', 30, false, '2023-01-02 11:00:00', ARRAY['user'], '{"role": "user", "level": 3}'),
		('Charlie', 'charlie@example.com', 35, true, '2023-01-03 12:00:00', ARRAY['user', 'moderator'], '{"role": "moderator", "level": 4}'),
		('Diana', 'diana@example.com', 28, true, '2023-01-04 13:00:00', ARRAY['user'], '{"role": "user", "level": 2}'),
		('Eve', 'eve@example.com', 22, false, '2023-01-05 14:00:00', ARRAY['guest'], '{"role": "guest", "level": 1}'),
		('Frank', 'frank@example.com', 40, true, '2023-01-06 15:00:00', ARRAY['user', 'admin'], '{"role": "admin", "level": 6}'),
		('Grace', 'grace@example.com', 32, true, '2023-01-07 16:00:00', ARRAY['user'], '{"role": "user", "level": 3}'),
		('Henry', 'henry@example.com', 45, false, '2023-01-08 17:00:00', ARRAY['guest'], '{"role": "guest", "level": 1}'),
		('Ivy', 'ivy@example.com', 27, true, '2023-01-09 18:00:00', ARRAY['user', 'moderator'], '{"role": "moderator", "level": 4}'),
		('Jack', 'jack@example.com', 33, true, '2023-01-10 19:00:00', ARRAY['user'], '{"role": "user", "level": 3}')
	`)
	require.NoError(t, err)

	return pool
}

func buildParams(t *testing.T, pageSize int64) *PaginationParams {
	t.Helper()

	params, err := NewBuilder().
		Table("test_models").
		Model(&TestModel{}).
		PageSize(pageSize).
		Build()
	require.NoError(t, err)
	return params
}

func buildParamsWithSort(t *testing.T, pageSize int64, sortColumn string, sortDirection queryPB.SortOrder) *PaginationParams {
	t.Helper()

	params, err := NewBuilder().
		Table("test_models").
		Model(&TestModel{}).
		PageSize(pageSize).
		SortBy(sortColumn, sortDirection).
		Build()
	require.NoError(t, err)
	return params
}

func executeQuery(t *testing.T, pool *pgxpool.Pool, params *PaginationParams) ([]TestModel, string, []any) {
	t.Helper()

	qb := NewQueryBuilder(params)
	sql, args, err := qb.BuildSQL()
	require.NoError(t, err)

	t.Logf("SQL: %s", sql)
	t.Logf("Args: %v", args)

	rows, err := pool.Query(t.Context(), sql, args...)
	require.NoError(t, err)
	defer rows.Close()

	models, err := ScanRows[TestModel](rows)
	require.NoError(t, err)

	return models, sql, args
}

func assertSortedResults(t *testing.T, models []TestModel, expectedIDs []int64) {
	t.Helper()

	require.GreaterOrEqual(t, len(models), len(expectedIDs), "Not enough models to verify sorting")

	for i, expectedID := range expectedIDs {
		if i < len(models) {
			require.Equal(t, expectedID, models[i].ID, "Sorting mismatch at index %d", i)
		}
	}
}

func TestQueryBuilder_DefaultCursorColumnNoSort(t *testing.T) {
	t.Parallel()

	t.Run("orders without token pagination", func(t *testing.T) {
		t.Parallel()

		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			Build()
		require.NoError(t, err)

		sql, args, err := NewQueryBuilder(params).BuildSQL()
		require.NoError(t, err)
		require.Empty(t, args)
		require.Contains(t, sql, `ORDER BY "test_models"."id" ASC`)
	})

	t.Run("prefers id column", func(t *testing.T) {
		t.Parallel()

		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			WithTokenPagination().
			Build()
		require.NoError(t, err)

		sql, args, err := NewQueryBuilder(params).BuildSQL()
		require.NoError(t, err)
		require.Empty(t, args)
		require.Contains(t, sql, `ORDER BY "test_models"."id" ASC`)

		token, err := GenerateNextTokenFromModel(&TestModel{ID: 123}, params)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		decoded, err := decodeToken(token)
		require.NoError(t, err)
		require.Equal(t, []string{"id"}, decoded.SortColumns)
		require.Equal(t, float64(123), decoded.CursorValues["id"])

		paramsWithToken, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			WithTokenPagination().
			PageToken(token).
			Build()
		require.NoError(t, err)

		sql, args, err = NewQueryBuilder(paramsWithToken).BuildSQL()
		require.NoError(t, err)
		require.Len(t, args, 1)
		require.Contains(t, sql, `WHERE ("test_models"."id" > $1)`)
		require.Contains(t, sql, `ORDER BY "test_models"."id" ASC`)
		// the outer select over the candidates CTE must be ordered by the
		// projected column names so the last row is the cursor position
		require.Contains(t, sql, `SELECT * FROM candidates ORDER BY "id" ASC`)
	})

	t.Run("token uses provided model when params model is nil", func(t *testing.T) {
		t.Parallel()

		params := NewPaginationParams()
		params.PageSize = 3
		params.WithTokenPagination = true

		token, err := GenerateNextTokenFromModel(&TestModel{ID: 123}, params)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		decoded, err := decodeToken(token)
		require.NoError(t, err)
		require.Equal(t, []string{"id"}, decoded.SortColumns)
		require.Equal(t, float64(123), decoded.CursorValues["id"])
	})

	t.Run("token unwraps double pointer model", func(t *testing.T) {
		t.Parallel()

		params := NewPaginationParams()
		params.PageSize = 3
		params.WithTokenPagination = true

		model := &TestModel{ID: 123}
		modelPtr := &model
		token, err := GenerateNextTokenFromModel(modelPtr, params)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		decoded, err := decodeToken(token)
		require.NoError(t, err)
		require.Equal(t, []string{"id"}, decoded.SortColumns)
		require.Equal(t, float64(123), decoded.CursorValues["id"])
	})

	t.Run("response token handles pointer models", func(t *testing.T) {
		t.Parallel()

		params := NewPaginationParams()
		params.PageSize = 3
		params.WithTokenPagination = true

		models := []*TestModel{
			{ID: 1},
			{ID: 2},
			{ID: 3},
			{ID: 4},
		}

		trimmedModels, response, err := CreatePaginationResponse(models, nil, params)
		require.NoError(t, err)
		require.Len(t, trimmedModels, 3)
		require.NotEmpty(t, response.NextPageToken)

		decoded, err := decodeToken(response.NextPageToken)
		require.NoError(t, err)
		require.Equal(t, []string{"id"}, decoded.SortColumns)
		require.Equal(t, float64(3), decoded.CursorValues["id"])
	})

	t.Run("token fingerprint ignores page token", func(t *testing.T) {
		t.Parallel()

		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(1).
			WithTokenPagination().
			Build()
		require.NoError(t, err)

		token, err := GenerateNextTokenFromModel(&TestModel{ID: 1}, params)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		nextParams, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(1).
			WithTokenPagination().
			PageToken(token).
			Build()
		require.NoError(t, err)

		require.NoError(t, validateToken(token, nextParams))
	})

	t.Run("token fingerprint includes built table state", func(t *testing.T) {
		t.Parallel()

		requestParams := NewPaginationParams()
		requestParams.PageSize = 1
		requestParams.WithTokenPagination = true

		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			FromPaginationParams(requestParams).
			Build()
		require.NoError(t, err)

		token, err := GenerateNextTokenFromModel(&TestModel{ID: 1}, params)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		nextParams, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			FromPaginationParams(requestParams).
			PageToken(token).
			Build()
		require.NoError(t, err)

		require.NoError(t, validateToken(token, nextParams))
	})

	t.Run("appends id tie breaker to sort", func(t *testing.T) {
		t.Parallel()

		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			SortBy("age", queryPB.SortOrder_SORT_ORDER_DESC).
			WithTokenPagination().
			Build()
		require.NoError(t, err)

		cursorColumns, err := resolveCursorColumns(params)
		require.NoError(t, err)
		require.Equal(t, []string{"age", "id"}, cursorColumns)

		sql, args, err := NewQueryBuilder(params).BuildSQL()
		require.NoError(t, err)
		require.Empty(t, args)
		require.Contains(t, sql, `ORDER BY "age" DESC, "test_models"."id" DESC`)

		token, err := GenerateNextTokenFromModel(&TestModel{ID: 123, Age: 45}, params)
		require.NoError(t, err)

		decoded, err := decodeToken(token)
		require.NoError(t, err)
		require.Equal(t, []string{"age", "id"}, decoded.SortColumns)

		paramsWithToken, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			SortBy("age", queryPB.SortOrder_SORT_ORDER_DESC).
			WithTokenPagination().
			PageToken(token).
			Build()
		require.NoError(t, err)

		sql, args, err = NewQueryBuilder(paramsWithToken).BuildSQL()
		require.NoError(t, err)
		require.Len(t, args, 2)
		require.Contains(t, sql, `("test_models"."age", "test_models"."id") < ($1, $2)`)
		require.Contains(t, sql, `ORDER BY "age" DESC, "test_models"."id" DESC`)
		require.Contains(t, sql, `SELECT * FROM candidates ORDER BY "age" DESC, "id" DESC`)
	})

	t.Run("falls back to first model column", func(t *testing.T) {
		t.Parallel()

		params, err := NewBuilder().
			Table("cursor_fallback_models").
			Model(&CursorFallbackModel{}).
			PageSize(3).
			WithTokenPagination().
			Build()
		require.NoError(t, err)

		sql, args, err := NewQueryBuilder(params).BuildSQL()
		require.NoError(t, err)
		require.Empty(t, args)
		require.Contains(t, sql, `ORDER BY "cursor_fallback_models"."name" ASC`)

		token, err := GenerateNextTokenFromModel(&CursorFallbackModel{Name: "alpha"}, params)
		require.NoError(t, err)

		decoded, err := decodeToken(token)
		require.NoError(t, err)
		require.Equal(t, []string{"name"}, decoded.SortColumns)
		require.Equal(t, "alpha", decoded.CursorValues["name"])
	})
}

func assertFilterResults(t *testing.T, models []TestModel, filterFunc func(TestModel) bool) {
	t.Helper()

	for _, model := range models {
		require.True(t, filterFunc(model), "Model %v does not match filter criteria", model)
	}
}

func buildParamsWithJSONFilter(t *testing.T, pageSize int64, jsonColumn string, jsonValue string) *PaginationParams {
	t.Helper()

	params, err := NewBuilder().
		Table("test_models").
		Model(&TestModel{}).
		PageSize(pageSize).
		JSONContains(jsonColumn, jsonValue).
		Build()
	require.NoError(t, err)
	return params
}

func buildParamsWithArrayFilter(t *testing.T, pageSize int64, arrayColumn string, arrayValue any) *PaginationParams {
	t.Helper()

	params, err := NewBuilder().
		Table("test_models").
		Model(&TestModel{}).
		PageSize(pageSize).
		ArrayContains(arrayColumn, arrayValue).
		Build()
	require.NoError(t, err)
	return params
}

func assertJSONFilterResults(t *testing.T, models []TestModel, jsonField string, expectedValue any) {
	t.Helper()

	for _, model := range models {
		value, exists := model.Metadata[jsonField]
		require.True(t, exists, "Model %v missing JSON field %s", model, jsonField)
		require.Equal(t, expectedValue, value, "Model %v has wrong value for %s", model, jsonField)
	}
}

func assertArrayFilterResults(t *testing.T, models []TestModel, arrayValue string) {
	t.Helper()

	for _, model := range models {
		found := slices.Contains(model.Tags, arrayValue)
		require.True(t, found, "Model %v tags do not contain %s", model, arrayValue)
	}
}

func TestQueryBuilder_BuildSQL(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("basic query", func(t *testing.T) {
		params := buildParams(t, 5)

		allModels, sql, args := executeQuery(t, pool, params)
		require.Len(t, allModels, 6) // should fetch PageSize + 1 = 6

		require.Equal(t, int64(1), allModels[0].ID)
		require.Equal(t, "Alice", allModels[0].Name)

		require.Contains(t, sql, "SELECT test_models.id")
		require.Contains(t, sql, "LIMIT 6")
		require.Empty(t, args) // no args for basic query
	})

	t.Run("with sorting", func(t *testing.T) {
		params := buildParamsWithSort(t, 3, "age", queryPB.SortOrder_SORT_ORDER_DESC)

		allModels, sql, args := executeQuery(t, pool, params)
		require.Len(t, allModels, 4) // should fetch PageSize + 1 = 4

		// should be sorted by age descending
		assertSortedResults(t, allModels, []int64{8, 6, 3}) // Henry, Frank, Charlie

		// verify SQL structure
		require.Contains(t, sql, "ORDER BY \"age\" DESC")
		require.Contains(t, sql, "LIMIT 4")
		require.Empty(t, args) // no args for sorting only
	})

	t.Run("with filters", func(t *testing.T) {
		// build params with multiple filters
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			Eq("active", true).
			Gt("age", 25).
			Build()
		require.NoError(t, err)

		allModels, sql, args := executeQuery(t, pool, params)
		// only 6 records match the filter criteria (active=true AND age>25)
		require.Len(t, allModels, 6)

		// all should be active and age > 25
		assertFilterResults(t, allModels, func(model TestModel) bool {
			return model.Active && model.Age > 25
		})

		// verify SQL structure
		require.Contains(t, sql, "WHERE \"active\" = $1 AND \"age\" > $2")
		require.Contains(t, sql, "LIMIT 11")
		require.Len(t, args, 2) // two filter arguments
		require.Equal(t, true, args[0])
		require.Equal(t, 25, args[1])
	})

	t.Run("with JSON filters", func(t *testing.T) {
		params := buildParamsWithJSONFilter(t, 10, "metadata", `{"role": "admin"}`)

		models, sql, args := executeQuery(t, pool, params)

		// all should have admin role
		assertJSONFilterResults(t, models, "role", "admin")

		// verify SQL structure
		require.Contains(t, sql, "WHERE \"metadata\" @> $1::jsonb")
		require.Contains(t, sql, "LIMIT 11")
		require.Len(t, args, 1)
		require.Equal(t, `{"role": "admin"}`, args[0])
	})

	t.Run("with array filters", func(t *testing.T) {
		params := buildParamsWithArrayFilter(t, 10, "tags", "admin")

		models, sql, args := executeQuery(t, pool, params)

		// all should have admin tag
		assertArrayFilterResults(t, models, "admin")

		// verify SQL structure
		require.Contains(t, sql, "WHERE \"tags\" @> $1")
		require.Contains(t, sql, "LIMIT 11")
		require.Len(t, args, 1)
		// convert any slice to string slice for comparison
		argSlice, argSliceOK := args[0].([]any)
		if !argSliceOK {
			t.Fatal("expected []any")
		}

		stringSlice := make([]string, len(argSlice))
		for i, v := range argSlice {
			stringValue, stringValueOK := v.(string)
			if !stringValueOK {
				t.Fatal("expected string")
			}
			stringSlice[i] = stringValue
		}
		require.Equal(t, []string{"admin"}, stringSlice)
	})
}

func TestQueryBuilder_BuildCountSQL(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("exact count", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			Eq("active", true).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildCountSQL()
		require.NoError(t, err)

		t.Logf("Count SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		_, response, err := CreatePaginationResponse([]TestModel{}, rows, params)
		require.NoError(t, err)

		require.Equal(t, int64(7), response.TotalCount) // 7 active users
	})

	t.Run("count estimate", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			Eq("active", true).
			WithCountEstimate().
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildCountSQL()
		require.NoError(t, err)

		t.Logf("Estimate SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		_, response, err := CreatePaginationResponse([]TestModel{}, rows, params)
		require.NoError(t, err)

		require.Greater(t, response.TotalCount, int64(0)) // should have some estimate
	})
}

func TestCreatePaginationResponse(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("has more detection", func(t *testing.T) {
		// test with PageSize + 1
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			Skip(0).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		allModels, err := ScanRows[TestModel](rows)
		require.NoError(t, err)
		require.Len(t, allModels, 4) // should fetch PageSize + 1 = 4

		// create count query for accurate HasMore detection
		countSQL, countArgs, err := qb.BuildCountSQL()
		require.NoError(t, err)

		countRows, err := pool.Query(t.Context(), countSQL, countArgs...)
		require.NoError(t, err)

		_, response, err := CreatePaginationResponse(allModels, countRows, params)
		require.NoError(t, err)

		require.Equal(t, int64(3), response.PageSize)
		require.True(t, response.HasMore) // should have more records
		require.Equal(t, int64(10), response.TotalCount)
	})

	t.Run("no more records", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			Skip(0).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		allModels, err := ScanRows[TestModel](rows)
		require.NoError(t, err)
		require.Len(t, allModels, 10) // only 10 records exist in table

		countSQL, countArgs, err := qb.BuildCountSQL()
		require.NoError(t, err)

		countRows, err := pool.Query(t.Context(), countSQL, countArgs...)
		require.NoError(t, err)

		_, response, err := CreatePaginationResponse(allModels, countRows, params)
		require.NoError(t, err)

		require.False(t, response.HasMore) // no more records
		require.Equal(t, int64(10), response.TotalCount)
	})
}

func TestQueryBuilder_AdvancedSorting(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("JSON path sorting", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(5).
			SortByJSONPath("metadata", "level", queryPB.SortOrder_SORT_ORDER_DESC).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("JSON Path Sort SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// should be sorted by metadata.level descending
		require.Equal(t, int64(6), models[0].ID) // Frank, level 6
		require.Equal(t, int64(1), models[1].ID) // Alice, level 5
	})

	t.Run("array length sorting", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(5).
			SortByArrayLength("tags", queryPB.SortOrder_SORT_ORDER_DESC).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("Array Length Sort SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// should be sorted by array length descending (2 elements first)
		require.Len(t, models[0].Tags, 2) // first record should have 2 tags
		require.Len(t, models[1].Tags, 2) // second record should have 2 tags
	})

	t.Run("array contains sorting", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(5).
			SortByArrayContains("tags", "admin", queryPB.SortOrder_SORT_ORDER_ASC).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("Array Contains Sort SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// records without "admin" should come first (position = 0)
		// records with "admin" should come after (position > 0)
		hasAdminCount := 0
		for _, model := range models {
			if slices.Contains(model.Tags, "admin") {
				hasAdminCount++
			}
		}
		// should have some records with admin and some without
		require.Greater(t, hasAdminCount, 0)
		require.Less(t, hasAdminCount, len(models))
	})
}

func TestQueryBuilder_TokenPagination(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("generate token", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			SortBy("id", queryPB.SortOrder_SORT_ORDER_ASC).
			WithTokenPagination().
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		allModels, err := ScanRows[TestModel](rows)
		require.NoError(t, err)
		require.Len(t, allModels, 4) // should fetch PageSize + 1 = 4

		// generate token from last model (trimmed)
		token, err := GenerateNextTokenFromModel(&allModels[2], params)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		t.Logf("Generated token: %s", token)
	})

	t.Run("use token for next page", func(t *testing.T) {
		// First page
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			SortBy("id", queryPB.SortOrder_SORT_ORDER_ASC).
			WithTokenPagination().
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		allModels, err := ScanRows[TestModel](rows)
		require.NoError(t, err)
		require.Len(t, allModels, 4) // should fetch PageSize + 1 = 4

		// generate token
		token, err := GenerateNextTokenFromModel(&allModels[2], params)
		require.NoError(t, err)

		// Second page using token
		params2, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			SortBy("id", queryPB.SortOrder_SORT_ORDER_ASC).
			WithTokenPagination().
			PageToken(token).
			Build()
		require.NoError(t, err)

		qb2 := NewQueryBuilder(params2)
		sql2, args2, err := qb2.BuildSQL()
		require.NoError(t, err)

		t.Logf("Token Pagination SQL: %s", sql2)

		rows2, err := pool.Query(t.Context(), sql2, args2...)
		require.NoError(t, err)
		defer rows2.Close()

		models2, err := ScanRows[TestModel](rows2)
		require.NoError(t, err)
		require.Len(t, models2, 4) // should fetch PageSize + 1 = 4

		// should get records after the first page
		require.Greater(t, models2[0].ID, allModels[2].ID)
		require.Equal(t, int64(4), models2[0].ID) // Diana
		require.Equal(t, int64(5), models2[1].ID) // Eve
		require.Equal(t, int64(6), models2[2].ID) // Frank
	})

	t.Run("token pagination with joined table sharing cursor column", func(t *testing.T) {
		// Both sides of the join expose `id`; an unqualified cursor reference
		// would fail with "column reference \"id\" is ambiguous".
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			Join("INNER JOIN test_models AS t2 ON t2.id = test_models.id").
			WithTokenPagination().
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		page1, err := ScanRows[TestModel](rows)
		require.NoError(t, err)
		require.Len(t, page1, 4)

		token, err := GenerateNextTokenFromModel(&page1[2], params)
		require.NoError(t, err)

		params2, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			Join("INNER JOIN test_models AS t2 ON t2.id = test_models.id").
			WithTokenPagination().
			PageToken(token).
			Build()
		require.NoError(t, err)

		sql2, args2, err := NewQueryBuilder(params2).BuildSQL()
		require.NoError(t, err)

		rows2, err := pool.Query(t.Context(), sql2, args2...)
		require.NoError(t, err)
		defer rows2.Close()

		page2, err := ScanRows[TestModel](rows2)
		require.NoError(t, err)
		require.Len(t, page2, 4)
		require.Equal(t, int64(4), page2[0].ID)
	})
}

func TestQueryBuilder_ErrorCases(t *testing.T) {
	t.Parallel()
	t.Run("missing table", func(t *testing.T) {
		_, err := NewBuilder().
			Table("").
			Model(&TestModel{}).
			PageSize(5).
			Build()
		require.Error(t, err)
		require.Contains(t, err.Error(), "table cannot be empty")
	})

	t.Run("missing model", func(t *testing.T) {
		_, err := NewBuilder().
			Table("test_models").
			Model(nil).
			PageSize(5).
			Build()
		require.Error(t, err)
		require.Contains(t, err.Error(), "model is required")
	})

	t.Run("invalid page size", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(0).
			Build()
		require.NoError(t, err)

		// builder should set default page size when 0 is provided
		require.Equal(t, int64(10), params.PageSize) // DefaultLimit
	})
}

func TestQueryBuilder_FilterGroups(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("OR filter group - simple", func(t *testing.T) {
		// Create filter group: (name ILIKE '%alice%' OR name ILIKE '%bob%')
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%alice%"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%bob%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("OR Filter Group SQL: %s", sql)
		t.Logf("Args: %v", args)

		// Verify SQL structure
		require.Contains(t, sql, "WHERE")
		require.Contains(t, sql, "OR")
		require.Contains(t, sql, "ILIKE")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Should match Alice or Bob
		require.GreaterOrEqual(t, len(models), 2)
		names := make(map[string]bool)
		for _, model := range models {
			names[model.Name] = true
		}
		require.True(t, names["Alice"], "Should have Alice")
		require.True(t, names["Bob"], "Should have Bob")
	})

	t.Run("AND filter group", func(t *testing.T) {
		// Create filter group: (age > 30 AND active = true)
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_AND,
				Filters: []*queryPB.Filter{
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 30}},
					},
					{
						Column:   "active",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
						Values:   []*queryPB.FilterValue{{BoolValue: true}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("AND Filter Group SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// All should match: age > 30 AND active = true
		for _, model := range models {
			require.Greater(t, model.Age, 30, "Age should be > 30")
			require.True(t, model.Active, "Should be active")
		}
	})

	t.Run("regular filters AND filter group", func(t *testing.T) {
		// Regular filter: active = true
		// Filter group: (age > 25 OR age < 25)
		// Combined: active = true AND (age > 25 OR age < 25)
		// This should match all active records (since all ages are either > 25 or < 25, except age=25)
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			Eq("active", true).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 25}},
					},
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 25}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("Combined Filters SQL: %s", sql)

		// Verify both regular filter and filter group are present
		require.Contains(t, sql, "WHERE")
		require.Contains(t, sql, "AND")
		require.Contains(t, sql, "OR")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// All should be active
		for _, model := range models {
			require.True(t, model.Active, "Should be active")
		}
	})

	t.Run("multiple filter groups", func(t *testing.T) {
		// Group 1: (name ILIKE '%alice%' OR name ILIKE '%bob%')
		// Group 2: (age > 30 OR age < 25)
		// Combined with AND between groups
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%alice%"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%bob%"}},
					},
				},
			}).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 30}},
					},
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 25}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("Multiple Filter Groups SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Should match (Alice or Bob) AND (age > 30 or age < 25)
		// Alice is 25, Bob is 30, so neither should match the age condition
		// This should return 0 results
		require.Len(t, models, 0)
	})

	t.Run("empty filter group", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters:  []*queryPB.Filter{},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("Empty Filter Group SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Empty filter group should not filter anything
		require.Greater(t, len(models), 0)
	})

	t.Run("nil filter group", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(nil).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Nil filter group should not filter anything
		require.Greater(t, len(models), 0)
	})

	t.Run("filter group with IN operator", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "id",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_IN,
						Values: []*queryPB.FilterValue{
							{IntValue: 1},
							{IntValue: 2},
							{IntValue: 3},
						},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("IN Filter Group SQL: %s", sql)

		require.Contains(t, sql, "= ANY")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Should match IDs 1, 2, 3
		ids := make(map[int64]bool)
		for _, model := range models {
			ids[model.ID] = true
		}
		require.True(t, ids[1], "Should have ID 1")
		require.True(t, ids[2], "Should have ID 2")
		require.True(t, ids[3], "Should have ID 3")
	})

	t.Run("filter group with IS_NULL operator", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL,
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("IS_NULL Filter Group SQL: %s", sql)

		require.Contains(t, sql, "IS NULL")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// All test data has age values, so should return 0
		require.Len(t, models, 0)
	})

	t.Run("filter group with BETWEEN operator", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN,
						Values: []*queryPB.FilterValue{
							{IntValue: 25},
							{IntValue: 35},
						},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("BETWEEN Filter Group SQL: %s", sql)

		require.Contains(t, sql, "BETWEEN")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Should match ages between 25 and 35 inclusive
		for _, model := range models {
			require.GreaterOrEqual(t, model.Age, 25, "Age should be >= 25")
			require.LessOrEqual(t, model.Age, 35, "Age should be <= 35")
		}
	})

	t.Run("filter group with LIKE operator", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "email",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_LIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%@example.com"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("LIKE Filter Group SQL: %s", sql)

		require.Contains(t, sql, "LIKE")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// All should have email ending with @example.com
		for _, model := range models {
			require.Contains(t, model.Email, "@example.com")
		}
	})

	t.Run("filter group with NOT_EQUALS operator", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "active",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_NOT_EQUALS,
						Values:   []*queryPB.FilterValue{{BoolValue: true}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("NOT_EQUALS Filter Group SQL: %s", sql)

		require.Contains(t, sql, "!=")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// All should be inactive
		for _, model := range models {
			require.False(t, model.Active, "Should be inactive")
		}
	})

	t.Run("complex filter combination", func(t *testing.T) {
		// Regular filters: active = true
		// Filter group 1: (name ILIKE '%a%' OR name ILIKE '%b%')
		// Filter group 2: (age > 30 OR age < 25)
		// Combined: active = true AND (name ILIKE '%a%' OR name ILIKE '%b%') AND (age > 30 OR age < 25)
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			Eq("active", true).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%a%"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%b%"}},
					},
				},
			}).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 30}},
					},
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 25}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("Complex Filter SQL: %s", sql)

		// Verify all components are present
		require.Contains(t, sql, "WHERE")
		require.Contains(t, sql, "AND")
		require.Contains(t, sql, "OR")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// All should be active
		for _, model := range models {
			require.True(t, model.Active, "Should be active")
			// Name should contain 'a' or 'b'
			nameContains := strings.Contains(strings.ToLower(model.Name), "a") ||
				strings.Contains(strings.ToLower(model.Name), "b")
			require.True(t, nameContains, "Name should contain 'a' or 'b'")
			// Age should be > 30 or < 25
			ageCondition := model.Age > 30 || model.Age < 25
			require.True(t, ageCondition, "Age should be > 30 or < 25")
		}
	})
}

func TestQueryBuilder_FilterGroups_EdgeCases(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("filter group with nil filter", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					nil,
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%alice%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Should still work, ignoring the nil filter
		require.Greater(t, len(models), 0)
	})

	t.Run("filter group with empty column name", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
						Values:   []*queryPB.FilterValue{{StringValue: "test"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%alice%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		_, _, err = qb.BuildSQL()
		require.Error(t, err)
	})

	t.Run("filter group with empty values", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
						Values:   []*queryPB.FilterValue{},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%alice%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		_, _, err = qb.BuildSQL()
		require.Error(t, err)
	})

	t.Run("filter group with UNSPECIFIED operator", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_UNSPECIFIED,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%alice%"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%bob%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		// UNSPECIFIED should default to AND
		require.Contains(t, sql, "AND")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// With AND, should match both Alice AND Bob (impossible for a single record)
		// So should return 0
		require.Len(t, models, 0)
	})

	t.Run("filter group with string value conversion", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "email",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
						Values:   []*queryPB.FilterValue{{StringValue: "alice@example.com"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		require.Len(t, models, 1)
		require.Equal(t, "Alice", models[0].Name)
	})

	t.Run("filter group with int value conversion", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
						Values:   []*queryPB.FilterValue{{IntValue: 25}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		require.Len(t, models, 1)
		require.Equal(t, "Alice", models[0].Name)
	})

	t.Run("filter group with bool value conversion", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "active",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
						Values:   []*queryPB.FilterValue{{BoolValue: true}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Should match all active users
		for _, model := range models {
			require.True(t, model.Active)
		}
	})
}

func TestQueryBuilder_ComprehensivePagination(t *testing.T) {
	t.Parallel()
	pool := setupTestDB(t)
	defer pool.Close()

	t.Run("full pagination cycle with filter groups", func(t *testing.T) {
		// First page with OR filter group
		params1, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			Skip(0).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%a%"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%e%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb1 := NewQueryBuilder(params1)
		sql1, args1, err := qb1.BuildSQL()
		require.NoError(t, err)

		rows1, err := pool.Query(t.Context(), sql1, args1...)
		require.NoError(t, err)
		defer rows1.Close()

		models1, err := ScanRows[TestModel](rows1)
		require.NoError(t, err)

		// Second page
		params2, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(3).
			Skip(4). // Skip past the first page's 3 items + 1 hasMore item
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%a%"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%e%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb2 := NewQueryBuilder(params2)
		sql2, args2, err := qb2.BuildSQL()
		require.NoError(t, err)

		rows2, err := pool.Query(t.Context(), sql2, args2...)
		require.NoError(t, err)
		defer rows2.Close()

		models2, err := ScanRows[TestModel](rows2)
		require.NoError(t, err)

		// Verify pagination works correctly
		// Names containing 'a' or 'e': Alice, Charlie, Diana, Eve, Frank, Grace, Henry, Jack
		// First 3 + 1 (hasMore): Alice, Charlie, Diana, Eve
		// Next 3 + 1 (hasMore): Frank, Grace, Henry, Jack
		require.Len(t, models1, 4) // PageSize + 1 for hasMore detection
		require.Len(t, models2, 4) // PageSize + 1 for hasMore detection

		// Verify different results
		names1 := make([]string, len(models1))
		for i, m := range models1 {
			names1[i] = m.Name
		}
		names2 := make([]string, len(models2))
		for i, m := range models2 {
			names2[i] = m.Name
		}

		// Names should not overlap
		overlap := false
		for _, n1 := range names1 {
			if slices.Contains(names2, n1) {
				overlap = true
			}
		}
		require.False(t, overlap, "Pages should not overlap")
	})

	t.Run("filter groups with sorting", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			PageSize(10).
			SortBy("age", queryPB.SortOrder_SORT_ORDER_DESC).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "active",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
						Values:   []*queryPB.FilterValue{{BoolValue: true}},
					},
					{
						Column:   "age",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
						Values:   []*queryPB.FilterValue{{IntValue: 40}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildSQL()
		require.NoError(t, err)

		t.Logf("Filter Groups with Sorting SQL: %s", sql)

		require.Contains(t, sql, "ORDER BY")
		require.Contains(t, sql, "DESC")

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		models, err := ScanRows[TestModel](rows)
		require.NoError(t, err)

		// Verify sorting
		for i := 1; i < len(models); i++ {
			require.GreaterOrEqual(t, models[i-1].Age, models[i].Age, "Should be sorted descending by age")
		}

		// Verify filter matches
		for _, model := range models {
			match := model.Active || model.Age > 40
			require.True(t, match, "Should match filter group")
		}
	})

	t.Run("filter groups with count query", func(t *testing.T) {
		params, err := NewBuilder().
			Table("test_models").
			Model(&TestModel{}).
			FilterGroup(&FilterGroup{
				Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
				Filters: []*queryPB.Filter{
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%a%"}},
					},
					{
						Column:   "name",
						Operator: queryPB.FilterOperator_FILTER_OPERATOR_ILIKE,
						Values:   []*queryPB.FilterValue{{StringValue: "%e%"}},
					},
				},
			}).
			Build()
		require.NoError(t, err)

		qb := NewQueryBuilder(params)
		sql, args, err := qb.BuildCountSQL()
		require.NoError(t, err)

		t.Logf("Filter Groups Count SQL: %s", sql)

		rows, err := pool.Query(t.Context(), sql, args...)
		require.NoError(t, err)
		defer rows.Close()

		_, response, err := CreatePaginationResponse([]TestModel{}, rows, params)
		require.NoError(t, err)

		// Should count records with 'a' or 'e' in name
		// Names: Alice(a), Charlie(a,e), Diana(a), Eve(e), Frank(a), Grace(a,e), Henry(e), Jack(a)
		// That's 8 records
		require.Equal(t, int64(8), response.TotalCount)
	})
}

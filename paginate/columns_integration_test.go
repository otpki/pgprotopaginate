package paginate

import (
	"testing"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupAnnotatedDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newTestPool(t)
	_, err := pool.Exec(t.Context(), `
        CREATE TABLE categories (id BIGINT PRIMARY KEY, label TEXT);
        INSERT INTO categories VALUES (1, 'A'), (2, 'B');
        CREATE TABLE mapped_rows (id BIGINT PRIMARY KEY, db_name TEXT, score BIGINT, status INTEGER, category_id BIGINT);
        INSERT INTO mapped_rows VALUES
          (1, 'alpha', 10, 1, 1), (2, 'beta', 10, 2, 2),
          (3, 'gamma', 20, 1, 1), (4, 'delta', 30, 2, 2);
    `)
	require.NoError(t, err)
	return pool
}

func TestAnnotatedPaginationResults(t *testing.T) {
	t.Parallel()
	pool := setupAnnotatedDB(t)
	score := func(value int64) *queryPB.Filter {
		return &queryPB.Filter{Column: "details.score", Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN, Values: []*queryPB.FilterValue{{IntValue: value}}}
	}
	for _, tc := range []struct {
		name    string
		request *queryPB.PaginationRequest
		ids     []int64
	}{
		{"nested filter", &queryPB.PaginationRequest{Filters: []*queryPB.Filter{score(10)}}, []int64{3, 4}},
		{"repeated nested filters", &queryPB.PaginationRequest{Filters: []*queryPB.Filter{score(20), score(5)}}, []int64{4}},
		{"nested OR group", &queryPB.PaginationRequest{FilterGroups: []*queryPB.FilterGroup{{Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR, Filters: []*queryPB.Filter{
			score(20), {Column: "name", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{StringValue: "beta"}}},
		}}}}, []int64{2, 4}},
		{"renamed sort", &queryPB.PaginationRequest{Sort: []*queryPB.SortEntry{{Column: "name", Order: queryPB.SortOrder_SORT_ORDER_DESC}}}, []int64{3, 4, 2, 1}},
		{"nested enum", &queryPB.PaginationRequest{Filters: []*queryPB.Filter{{Column: "details.status", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{IntValue: 1}}}}}, []int64{1, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, page, err := ProtoPaginate(t.Context(), pool, nil, "mapped_rows", tc.request, annotatedRowToProto)
			require.NoError(t, err)
			ids := make([]int64, len(rows))
			for i, row := range rows {
				ids[i] = annotatedID(row)
			}
			require.Equal(t, tc.ids, ids)
			require.Equal(t, int64(len(ids)), page.TotalCount)
		})
	}
	for _, column := range []string{"name", "details.score", "id"} {
		t.Run("cursor "+column, func(t *testing.T) {
			request := &queryPB.PaginationRequest{PageSize: 2, WithTokenPagination: true, Sort: []*queryPB.SortEntry{{Column: column}}}
			var ids []int64
			for range 3 {
				rows, page, err := ProtoPaginate(t.Context(), pool, nil, "mapped_rows", request, annotatedRowToProto)
				require.NoError(t, err)
				for _, row := range rows {
					ids = append(ids, annotatedID(row))
				}
				if !page.HasMore {
					break
				}
				require.NotEmpty(t, page.NextPageToken)
				token, err := decodeToken(page.NextPageToken)
				require.NoError(t, err)
				if column == "id" {
					require.Len(t, token.SortColumns, 1, "qualified id must not add a duplicate tie breaker")
				}
				request.PageToken = page.NextPageToken
			}
			require.Len(t, ids, 4)
			require.ElementsMatch(t, []int64{1, 2, 3, 4}, ids)
			if column == "details.score" {
				require.Equal(t, []int64{1, 2, 3, 4}, ids)
			}
			if column == "name" {
				require.Equal(t, []int64{1, 2, 4, 3}, ids)
			}
		})
	}
}

func TestAnnotatedJoinedPagination(t *testing.T) {
	t.Parallel()
	pool := setupAnnotatedDB(t)
	join := &ExtendedJoinConfig{
		Joins:             []string{"JOIN categories c ON c.id = mapped_rows.category_id"},
		AdditionalColumns: []string{"c.label AS joined_label"},
	}
	convert := func(row *annotatedDBRow, _ map[string]any) (*annotatedProto, error) {
		return annotatedRowToProto(row)
	}
	rows, page, err := ProtoPaginateExtended(t.Context(), pool, nil, "mapped_rows", &queryPB.PaginationRequest{
		Filters: []*queryPB.Filter{{Column: "details.category", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{StringValue: "B"}}}},
	}, convert, join)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, int64(2), annotatedID(rows[0]))
	require.Equal(t, int64(4), annotatedID(rows[1]))
	require.Equal(t, int64(2), page.TotalCount)

	_, info, err := ProtoPaginateExtended(t.Context(), nil, nil, "mapped_rows", &queryPB.PaginationRequest{WithPaginationInfoOnly: true}, convert, join)
	require.NoError(t, err)
	found := false
	for _, column := range info.PaginationInfo.Columns {
		if column.Column == "details.category" {
			found = true
			require.True(t, column.Filterable)
			require.True(t, column.Sortable)
		}
	}
	require.True(t, found)

	request := &queryPB.PaginationRequest{PageSize: 2, WithTokenPagination: true, Sort: []*queryPB.SortEntry{{Column: "details.category"}}}
	rows, page, err = ProtoPaginateExtended(t.Context(), pool, nil, "mapped_rows", request, convert, join)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, int64(1), annotatedID(rows[0]))
	require.Equal(t, int64(3), annotatedID(rows[1]))
	require.True(t, page.HasMore)
	require.NotEmpty(t, page.NextPageToken)
	request.PageToken = page.NextPageToken
	rows, page, err = ProtoPaginateExtended(t.Context(), pool, nil, "mapped_rows", request, convert, join)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, int64(2), annotatedID(rows[0]))
	require.Equal(t, int64(4), annotatedID(rows[1]))
	require.False(t, page.HasMore)
}

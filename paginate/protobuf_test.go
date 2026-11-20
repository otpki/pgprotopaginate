package paginate

import (
	"errors"
	"testing"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// Use the request message as a result model so these tests also exercise the
// Buf-generated types without introducing a separate test schema.
type paginationRow struct {
	PageSize int64 `db:"page_size"`
	Skip     int64 `db:"skip"`
}

func paginationRowToProto(row *paginationRow) (*queryPB.PaginationRequest, error) {
	return &queryPB.PaginationRequest{PageSize: row.PageSize, Skip: row.Skip}, nil
}

func TestProtoPaginateCounts(t *testing.T) {
	t.Parallel()
	pool := newTestPool(t)
	_, err := pool.Exec(t.Context(), `
		CREATE TABLE pagination_rows (page_size BIGINT, skip BIGINT);
		INSERT INTO pagination_rows VALUES (10, 0), (20, 1);
	`)
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		query   *queryPB.PaginationRequest
		count   int64
		wantErr error
	}{
		{name: "nil request", count: 2},
		{name: "empty request", query: &queryPB.PaginationRequest{}, count: 2},
		{name: "without count", query: &queryPB.PaginationRequest{WithoutCount: true}},
		{name: "negative page size", query: &queryPB.PaginationRequest{PageSize: -1}, wantErr: ErrInvalidRequest},
		{name: "oversized page", query: &queryPB.PaginationRequest{PageSize: MaxLimit + 1}, wantErr: ErrInvalidRequest},
		{name: "negative skip", query: &queryPB.PaginationRequest{Skip: -1}, wantErr: ErrInvalidRequest},
		{name: "invalid sort", query: &queryPB.PaginationRequest{Sort: []*queryPB.SortEntry{{Column: "missing"}}}, wantErr: ErrInvalidRequest},
		{name: "missing filter value", query: &queryPB.PaginationRequest{Filters: []*queryPB.Filter{{Column: "page_size", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS}}}, wantErr: ErrInvalidRequest},
		{
			name: "invalid group",
			query: &queryPB.PaginationRequest{FilterGroups: []*queryPB.FilterGroup{{
				Filters: []*queryPB.Filter{{Column: "missing"}},
			}}},
			wantErr: ErrInvalidRequest,
		},
		{name: "invalid token", query: &queryPB.PaginationRequest{WithTokenPagination: true, PageToken: "not-base64"}, wantErr: ErrInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, page, err := ProtoPaginate(t.Context(), pool, nil, "pagination_rows", tc.query, paginationRowToProto)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, data, 2)
			require.Equal(t, int64(10), data[0].GetPageSize())
			require.Equal(t, tc.count, page.GetTotalCount())
			require.False(t, page.GetHasMore())
		})
	}
	t.Run("database error", func(t *testing.T) {
		_, _, err := ProtoPaginate(t.Context(), pool, nil, "missing_table", nil, paginationRowToProto)
		require.NotErrorIs(t, err, ErrInvalidRequest)
		var databaseError *pgconn.PgError
		require.ErrorAs(t, err, &databaseError)
		require.Equal(t, "42P01", databaseError.Code)
	})
	t.Run("conversion error", func(t *testing.T) {
		conversionError := errors.New("cannot convert row")
		_, _, err := ProtoPaginate(t.Context(), pool, nil, "pagination_rows", nil, func(*paginationRow) (*queryPB.PaginationRequest, error) {
			return nil, conversionError
		})
		require.ErrorIs(t, err, conversionError)
		require.NotErrorIs(t, err, ErrInvalidRequest)
	})
}

func TestProtoPaginateReusesCallerTransaction(t *testing.T) {
	t.Parallel()
	pool := newTestPool(t)
	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(t.Context()) })

	// This table is only visible inside the uncommitted transaction.
	_, err = tx.Exec(t.Context(), `
		CREATE TABLE transaction_rows (page_size BIGINT, skip BIGINT);
		INSERT INTO transaction_rows VALUES (10, 0), (20, 1);
	`)
	require.NoError(t, err)
	data, page, err := ProtoPaginate(t.Context(), tx, nil, "transaction_rows", &queryPB.PaginationRequest{PageSize: 1}, paginationRowToProto)
	require.NoError(t, err)
	require.Len(t, data, 1)
	require.True(t, page.GetHasMore())
	require.Equal(t, int64(2), page.GetTotalCount())

	// Pagination must leave the caller's transaction open and uncommitted.
	_, err = tx.Exec(t.Context(), "INSERT INTO transaction_rows VALUES (30, 2)")
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(t.Context()))
	var tableExists bool
	err = pool.QueryRow(t.Context(), "SELECT to_regclass('transaction_rows') IS NOT NULL").Scan(&tableExists)
	require.NoError(t, err)
	require.False(t, tableExists)
}

func TestJoinedBaseColumns(t *testing.T) {
	t.Parallel()
	pool := newTestPool(t)
	_, err := pool.Exec(t.Context(), `
		CREATE TABLE base_rows (page_size BIGINT PRIMARY KEY, skip BIGINT);
		CREATE TABLE related_rows (page_size BIGINT PRIMARY KEY, skip BIGINT);
		INSERT INTO base_rows VALUES (1, 1), (2, 2), (3, 1);
		INSERT INTO related_rows VALUES (1, 9), (2, 9), (3, 9);
	`)
	require.NoError(t, err)
	for _, grouped := range []bool{false, true} {
		filter := &queryPB.Filter{
			Column: "skip", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
			Values: []*queryPB.FilterValue{{IntValue: 1}},
		}
		request := &queryPB.PaginationRequest{
			PageSize: 1, WithTokenPagination: true,
			Sort:    []*queryPB.SortEntry{{Column: "page_size", Order: queryPB.SortOrder_SORT_ORDER_DESC}},
			Filters: []*queryPB.Filter{filter},
		}
		if grouped {
			request.Filters = nil
			request.FilterGroups = []*queryPB.FilterGroup{{Filters: []*queryPB.Filter{filter}}}
		}
		var ids []int64
		for range 2 {
			rows, page, err := ProtoPaginateExtended(t.Context(), pool, nil, "base_rows", request,
				func(row *paginationRow, _ map[string]any) (*queryPB.PaginationRequest, error) {
					return paginationRowToProto(row)
				}, &ExtendedJoinConfig{Joins: []string{"JOIN related_rows r ON r.page_size = base_rows.page_size"}})
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Equal(t, int64(2), page.TotalCount)
			ids = append(ids, rows[0].PageSize)
			request.PageToken = page.NextPageToken
		}
		require.Equal(t, []int64{3, 1}, ids)
	}
}

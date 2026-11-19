package paginate

import (
	"testing"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
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
		name  string
		query *queryPB.PaginationRequest
		count int64
	}{
		{name: "nil request", count: 2},
		{name: "empty request", query: &queryPB.PaginationRequest{}, count: 2},
		{name: "without count", query: &queryPB.PaginationRequest{WithoutCount: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, page, err := ProtoPaginate(t.Context(), pool, nil, "pagination_rows", tc.query, paginationRowToProto)
			require.NoError(t, err)
			require.Len(t, data, 2)
			require.Equal(t, int64(10), data[0].GetPageSize())
			require.Equal(t, tc.count, page.GetTotalCount())
			require.False(t, page.GetHasMore())
		})
	}
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

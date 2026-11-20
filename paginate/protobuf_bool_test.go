package paginate

import (
	"database/sql"
	"testing"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type booleanRow struct {
	PageSize     int64 `db:"page_size"`
	WithoutCount bool  `db:"without_count"`
}

func booleanRowToProto(row *booleanRow) (*queryPB.PaginationRequest, error) {
	return &queryPB.PaginationRequest{
		PageSize:     row.PageSize,
		WithoutCount: row.WithoutCount,
	}, nil
}

func TestBooleanFilterValues(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`{"boolValue":false}`, `{"boolValue":true}`} {
		t.Run(input, func(t *testing.T) {
			value := &queryPB.FilterValue{}
			require.NoError(t, protojson.Unmarshal([]byte(input), value))
			wire, err := proto.Marshal(value)
			require.NoError(t, err)
			require.NoError(t, proto.Unmarshal(wire, value))
			request := &queryPB.PaginationRequest{Filters: []*queryPB.Filter{{
				Column: "without_count", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
				Values: []*queryPB.FilterValue{value},
			}}}
			params, err := ConvertProtoRequest[*queryPB.PaginationRequest](request)
			require.NoError(t, err)
			require.Equal(t, []any{value.BoolValue}, params.Eq["without_count"])
		})
	}
	// Empty values must still be rejected for fields whose type is not boolean.
	_, err := ConvertProtoRequest[*queryPB.PaginationRequest](&queryPB.PaginationRequest{
		Filters: []*queryPB.Filter{{Column: "page_size", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{}}}},
	})
	require.Error(t, err)
}

func TestGroupedFalseFilterModelTypes(t *testing.T) {
	t.Parallel()
	model := &struct {
		hidden   bool
		Active   bool         `db:"active"`
		Pointer  *bool        `db:"pointer"`
		PgBool   pgtype.Bool  `db:"pg_bool"`
		Nullable sql.NullBool `db:"nullable"`
	}{}
	for _, column := range []string{"active", "pointer", "pg_bool", "nullable"} {
		builder := NewBuilder().Table("boolean_rows").Model(model).FilterGroup(&FilterGroup{
			Filters: []*queryPB.Filter{{Column: "boolean_rows." + column, Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{BoolValue: false}}}},
		})
		_, args, err := builder.BuildSQL()
		require.NoError(t, err)
		require.Equal(t, []any{false}, args)
	}
	// Joined columns use their supplied scan type rather than the base model.
	builder := NewBuilder().Table("boolean_rows").Model(&booleanRow{}).
		AdditionalSelect("related.active AS joined_active").
		AdditionalColumnTypes(map[string]any{"joined_active": pgtype.Bool{}}).
		FilterGroup(&FilterGroup{Filters: []*queryPB.Filter{{
			Column: "related.active", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
			Values: []*queryPB.FilterValue{{BoolValue: false}},
		}}})
	_, args, err := builder.BuildSQL()
	require.NoError(t, err)
	require.Equal(t, []any{false}, args)
}

func TestBooleanFilterResults(t *testing.T) {
	t.Parallel()
	pool := newTestPool(t)
	_, err := pool.Exec(t.Context(), `CREATE TABLE boolean_rows (page_size BIGINT PRIMARY KEY, without_count BOOLEAN NOT NULL);
		INSERT INTO boolean_rows VALUES (1, false), (2, true), (3, false);`)
	require.NoError(t, err)
	for _, grouped := range []bool{false, true} {
		filter := &queryPB.Filter{
			Column: "without_count", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
			Values: []*queryPB.FilterValue{{BoolValue: false}},
		}
		request := &queryPB.PaginationRequest{PageSize: 1, Filters: []*queryPB.Filter{filter}}
		if grouped {
			request.Filters = nil
			request.FilterGroups = []*queryPB.FilterGroup{{Filters: []*queryPB.Filter{filter}}}
		}
		rows, page, err := ProtoPaginate(t.Context(), pool, nil, "boolean_rows", request, booleanRowToProto)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.False(t, rows[0].WithoutCount)
		require.Equal(t, int64(2), page.TotalCount)
		require.True(t, page.HasMore)
		request.Skip = 1
		next, _, err := ProtoPaginate(t.Context(), pool, nil, "boolean_rows", request, booleanRowToProto)
		require.NoError(t, err)
		require.Len(t, next, 1)
		require.False(t, next[0].WithoutCount)
		require.NotEqual(t, rows[0].PageSize, next[0].PageSize)
	}
}

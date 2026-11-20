package paginate

import (
	"testing"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDefaultMessageAndMapPermissions(t *testing.T) {
	t.Parallel()
	fd := filterDescriptor(t)
	message := fd.Fields().ByName("details")
	mapField := (&structpb.Struct{}).ProtoReflect().Descriptor().Fields().ByName("fields")
	require.True(t, mapField.IsMap())
	for _, field := range []protoreflect.FieldDescriptor{message, mapField} {
		require.True(t, isFieldFilterable(field))
		require.True(t, isFieldSortable(field))
	}
	params := NewPaginationParams()
	require.NoError(t, convertFilters([]*queryPB.Filter{{Column: "details", Operator: queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL}}, params, fd, false))
	require.NoError(t, convertSortEntries([]*queryPB.SortEntry{{Column: "details"}}, params, fd))
	require.Equal(t, []string{"details"}, params.IsNull)
	// Querying the whole message does not implicitly expose its children.
	_, err := queryField(fd, "details.id")
	require.Error(t, err)
}

func TestUnknownLogicalOperatorUsesAND(t *testing.T) {
	t.Parallel()
	pool := setupAnnotatedDB(t)
	rows, page, err := ProtoPaginate(t.Context(), pool, nil, "mapped_rows", &queryPB.PaginationRequest{
		FilterGroups: []*queryPB.FilterGroup{{Operator: queryPB.LogicalOperator(999), Filters: []*queryPB.Filter{
			{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN, Values: []*queryPB.FilterValue{{IntValue: 1}}},
			{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_LESS_THAN, Values: []*queryPB.FilterValue{{IntValue: 4}}},
		}}},
	}, annotatedRowToProto)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, int64(2), annotatedID(rows[0]))
	require.Equal(t, int64(3), annotatedID(rows[1]))
	require.Equal(t, int64(2), page.TotalCount)
}

func TestUnaryFilterCardinality(t *testing.T) {
	t.Parallel()
	pool := setupAnnotatedDB(t)
	for _, tc := range []struct {
		operator queryPB.FilterOperator
		count    int64
	}{
		{queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL, 0},
		{queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_NULL, 4},
		{queryPB.FilterOperator_FILTER_OPERATOR_IS_EMPTY, 0},
		{queryPB.FilterOperator_FILTER_OPERATOR_IS_NOT_EMPTY, 4},
	} {
		t.Run(tc.operator.String(), func(t *testing.T) {
			for _, grouped := range []bool{false, true} {
				params := NewPaginationParams()
				filter := &queryPB.Filter{Column: "name", Operator: tc.operator, Values: []*queryPB.FilterValue{
					{StringValue: "ignored"}, nil, {TimestampValue: &timestamppb.Timestamp{Nanos: -1}},
				}}
				if grouped {
					require.Error(t, convertFilterGroups([]*queryPB.FilterGroup{{Filters: []*queryPB.Filter{filter}}}, params, annotatedDescriptor()))
				} else {
					require.Error(t, convertFilters([]*queryPB.Filter{filter}, params, annotatedDescriptor(), false))
				}
				filter.Values = nil
				if grouped {
					require.NoError(t, convertFilterGroups([]*queryPB.FilterGroup{{Filters: []*queryPB.Filter{filter}}}, params, annotatedDescriptor()))
				} else {
					require.NoError(t, convertFilters([]*queryPB.Filter{filter}, params, annotatedDescriptor(), false))
				}
				sql, args, err := NewBuilder().Table("mapped_rows").Model(annotatedDBRow{}).FromPaginationParams(params).BuildCountSQL()
				require.NoError(t, err)
				require.Empty(t, args)
				var count int64
				require.NoError(t, pool.QueryRow(t.Context(), sql, args...).Scan(&count))
				require.Equal(t, tc.count, count)
			}
		})
	}
}

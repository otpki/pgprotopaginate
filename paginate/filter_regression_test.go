package paginate

import (
	"testing"
	"time"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func filterDescriptor(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	hidden := &descriptorpb.FieldOptions{}
	proto.SetExtension(hidden, queryPB.E_Filterable, false)
	restricted := &descriptorpb.FieldOptions{}
	proto.SetExtension(restricted, queryPB.E_AllowedFilterOperators, []queryPB.FilterOperator{queryPB.FilterOperator_FILTER_OPERATOR_EQUALS})
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("filter_regression.proto"), Syntax: new("proto3"),
		Dependency: []string{"google/protobuf/timestamp.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("Row"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: new("id"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()},
				{Name: new("created_at"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".google.protobuf.Timestamp")},
				{Name: new("details"), Number: new(int32(3)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".Row")},
				{Name: new("hidden"), Number: new(int32(4)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Options: hidden},
				{Name: new("restricted"), Number: new(int32(5)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Options: restricted},
			},
		}},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)
	return file.Messages().Get(0)
}

func TestGroupedFilterValidation(t *testing.T) {
	fd := filterDescriptor(t)
	for _, tc := range []struct {
		name   string
		filter *queryPB.Filter
	}{
		{"unknown field", &queryPB.Filter{Column: "missing", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{StringValue: "x"}}}},
		{"disabled field", &queryPB.Filter{Column: "hidden", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{StringValue: "x"}}}},
		{"disallowed operator", &queryPB.Filter{Column: "restricted", Operator: queryPB.FilterOperator_FILTER_OPERATOR_LIKE, Values: []*queryPB.FilterValue{{StringValue: "x"}}}},
		{"missing value", &queryPB.Filter{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS}},
		{"nil value", &queryPB.Filter{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{nil}}},
		{"unknown operator", &queryPB.Filter{Column: "id", Operator: queryPB.FilterOperator(999), Values: []*queryPB.FilterValue{{IntValue: 1}}}},
		{"nil filter", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, convertFilters([]*queryPB.Filter{tc.filter}, NewPaginationParams(), fd, false))
			require.Error(t, convertFilterGroups([]*queryPB.FilterGroup{{Filters: []*queryPB.Filter{tc.filter}}}, NewPaginationParams(), fd))
		})
	}
	require.NoError(t, convertFilterGroups([]*queryPB.FilterGroup{{Operator: queryPB.LogicalOperator(999)}}, NewPaginationParams(), fd))
}

func TestRepeatedFilterPredicates(t *testing.T) {
	fd := filterDescriptor(t)
	for _, op := range []queryPB.FilterOperator{
		queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
		queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
		queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN,
		queryPB.FilterOperator_FILTER_OPERATOR_IN,
	} {
		t.Run(op.String(), func(t *testing.T) {
			filters := []*queryPB.Filter{
				{Column: "id", Operator: op, Values: []*queryPB.FilterValue{{IntValue: 1}}},
				{Column: "id", Operator: op, Values: []*queryPB.FilterValue{{IntValue: 3}}},
			}
			if op == queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN {
				filters[0].Values = append(filters[0].Values, &queryPB.FilterValue{IntValue: 5})
				filters[1].Values = append(filters[1].Values, &queryPB.FilterValue{IntValue: 7})
			}
			params := NewPaginationParams()
			require.NoError(t, convertFilters(filters, params, fd, false))
			builder := NewBuilder().Table("rows").Model(&struct {
				ID int64 `db:"id"`
			}{}).FromPaginationParams(params)
			sql, args, err := builder.BuildSQL()
			require.NoError(t, err)
			require.Contains(t, sql, " AND ")
			switch op {
			case queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN:
				require.Equal(t, []any{int64(1), int64(5), int64(3), int64(7)}, args)
			case queryPB.FilterOperator_FILTER_OPERATOR_IN:
				require.Equal(t, []any{[]any{int64(1)}, []any{int64(3)}}, args)
			default:
				require.Equal(t, []any{int64(1), int64(3)}, args)
			}
		})
	}
}

func TestGroupedTimestampAndCountSQL(t *testing.T) {
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	params := NewPaginationParams()
	require.NoError(t, convertFilterGroups([]*queryPB.FilterGroup{{
		Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR,
		Filters: []*queryPB.Filter{
			{Column: "created_at", Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN, Values: []*queryPB.FilterValue{{TimestampValue: timestamppb.New(cutoff)}}},
			{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_IN, Values: []*queryPB.FilterValue{{IntValue: 1}, {IntValue: 2}}},
		},
	}}, params, filterDescriptor(t)))
	builder := NewBuilder().Table("rows").Model(&struct {
		ID        int64     `db:"id"`
		CreatedAt time.Time `db:"created_at"`
	}{}).FromPaginationParams(params)
	sql, args, err := builder.BuildSQL()
	require.NoError(t, err)
	require.Contains(t, sql, " OR ")
	require.Contains(t, sql, "= ANY(")
	require.Equal(t, []any{cutoff, []any{int64(1), int64(2)}}, args)
	countSQL, countArgs, err := builder.BuildCountSQL()
	require.NoError(t, err)
	require.Contains(t, countSQL, " OR ")
	require.Equal(t, args, countArgs)
}

func TestPaginationMetadataSupportedFields(t *testing.T) {
	fd := filterDescriptor(t)
	require.True(t, isFieldFilterable(fd.Fields().ByName("created_at")))
	require.True(t, isFieldFilterable(fd.Fields().ByName("details")))
	require.True(t, isFieldSortable(fd.Fields().ByName("details")))
	response, err := GetPaginationInfoForModel[*queryPB.PaginationRequest, struct {
		PageSize int64 `db:"page_size"`
	}]()
	require.NoError(t, err)
	for _, column := range response.PaginationInfo.Columns {
		if column.Column == "page_size" {
			require.True(t, column.Filterable)
		} else {
			require.False(t, column.Filterable)
			require.False(t, column.Sortable)
		}
	}
	// The live pagination-info path uses the model and requires no database.
	_, response, err = ProtoPaginate(t.Context(), nil, nil, "rows", &queryPB.PaginationRequest{WithPaginationInfoOnly: true}, func(_ *struct {
		PageSize int64 `db:"page_size"`
	},
	) (*queryPB.PaginationRequest, error) {
		return nil, nil
	})
	require.NoError(t, err)
	for _, column := range response.PaginationInfo.Columns {
		require.Equal(t, column.Column == "page_size", column.Filterable)
	}
}

func TestGroupedTimestampResults(t *testing.T) {
	pool := newTestPool(t)
	_, err := pool.Exec(t.Context(), `CREATE TABLE dated_rows (id BIGINT, created_at TIMESTAMPTZ);
 INSERT INTO dated_rows VALUES (1,'2024-01-01Z'),(2,'2025-01-01Z'),(3,'2026-01-01Z'),(4,'2027-01-01Z');`)
	require.NoError(t, err)
	type row struct {
		ID        int64     `db:"id"`
		CreatedAt time.Time `db:"created_at"`
	}
	params := NewPaginationParams()
	require.NoError(t, convertFilterGroups([]*queryPB.FilterGroup{{Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR, Filters: []*queryPB.Filter{
		{Column: "created_at", Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN, Values: []*queryPB.FilterValue{{TimestampValue: timestamppb.New(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))}}},
		{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{{IntValue: 1}}},
	}}}, params, filterDescriptor(t)))
	builder := NewBuilder().Table("dated_rows").Model(&row{}).FromPaginationParams(params)
	sql, args, err := builder.BuildSQL()
	require.NoError(t, err)
	rows, err := pool.Query(t.Context(), sql, args...)
	require.NoError(t, err)
	records, err := ScanRows[row](rows)
	rows.Close()
	require.NoError(t, err)
	ids := make([]int64, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	require.ElementsMatch(t, []int64{1, 3, 4}, ids)
	sql, args, err = builder.BuildCountSQL()
	require.NoError(t, err)
	var count int64
	require.NoError(t, pool.QueryRow(t.Context(), sql, args...).Scan(&count))
	require.Equal(t, int64(3), count)
}

func TestGroupedFilterValueConversion(t *testing.T) {
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		value *queryPB.FilterValue
		want  any
	}{
		{"timestamp", &queryPB.FilterValue{TimestampValue: timestamppb.New(cutoff)}, cutoff},
		{"bytes", &queryPB.FilterValue{BytesValue: []byte{1, 2, 3}}, []byte{1, 2, 3}},
		{"duration", &queryPB.FilterValue{DurationValue: durationpb.New(time.Second)}, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			builder := NewBuilder().Table("rows").Model(&struct {
				ID int64 `db:"id"`
			}{}).
				FilterGroup(&FilterGroup{Filters: []*queryPB.Filter{{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{tc.value}}}})
			_, args, err := builder.BuildSQL()
			require.NoError(t, err)
			require.Equal(t, []any{tc.want}, args)
		})
	}
	for _, value := range []*queryPB.FilterValue{
		{TimestampValue: &timestamppb.Timestamp{Nanos: -1}},
		{DurationValue: &durationpb.Duration{Nanos: 1_000_000_000}},
	} {
		builder := NewBuilder().Table("rows").Model(&struct {
			ID int64 `db:"id"`
		}{}).
			FilterGroup(&FilterGroup{Filters: []*queryPB.Filter{{Column: "id", Operator: queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, Values: []*queryPB.FilterValue{value}}}})
		_, _, err := builder.BuildSQL()
		require.Error(t, err)
		_, _, err = builder.BuildCountSQL()
		require.Error(t, err)
	}
}

func TestRepeatedFilterResults(t *testing.T) {
	pool := newTestPool(t)
	_, err := pool.Exec(t.Context(), `CREATE TABLE numbered_rows (id BIGINT);
 INSERT INTO numbered_rows VALUES (1),(2),(3),(4),(5);`)
	require.NoError(t, err)
	for _, tc := range []struct {
		name          string
		operator      queryPB.FilterOperator
		first, second []int64
		want          []int64
	}{
		{"equality entries AND", queryPB.FilterOperator_FILTER_OPERATOR_EQUALS, []int64{1}, []int64{2}, []int64{}},
		{"IN values ANY", queryPB.FilterOperator_FILTER_OPERATOR_IN, []int64{1, 2}, nil, []int64{1, 2}},
		{"ranges retain stricter first predicate", queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN, []int64{3}, []int64{1}, []int64{4, 5}},
		{"between intersect", queryPB.FilterOperator_FILTER_OPERATOR_BETWEEN, []int64{1, 3}, []int64{2, 5}, []int64{2, 3}},
		{"in intersect", queryPB.FilterOperator_FILTER_OPERATOR_IN, []int64{1, 2, 3}, []int64{2, 3, 4}, []int64{2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filters := make([]*queryPB.Filter, 0, 2)
			for _, values := range [][]int64{tc.first, tc.second} {
				if values == nil {
					continue
				}
				filter := &queryPB.Filter{Column: "id", Operator: tc.operator}
				for _, value := range values {
					filter.Values = append(filter.Values, &queryPB.FilterValue{IntValue: value})
				}
				filters = append(filters, filter)
			}
			params := NewPaginationParams()
			require.NoError(t, convertFilters(filters, params, filterDescriptor(t), false))
			type row struct {
				ID int64 `db:"id"`
			}
			builder := NewBuilder().Table("numbered_rows").Model(&row{}).FromPaginationParams(params)
			sql, args, err := builder.BuildSQL()
			require.NoError(t, err)
			rows, err := pool.Query(t.Context(), sql, args...)
			require.NoError(t, err)
			records, err := ScanRows[row](rows)
			rows.Close()
			require.NoError(t, err)
			ids := make([]int64, 0, len(records))
			for _, record := range records {
				ids = append(ids, record.ID)
			}
			require.ElementsMatch(t, tc.want, ids)
			sql, args, err = builder.BuildCountSQL()
			require.NoError(t, err)
			var count int64
			require.NoError(t, pool.QueryRow(t.Context(), sql, args...).Scan(&count))
			require.Equal(t, int64(len(tc.want)), count)
		})
	}
}

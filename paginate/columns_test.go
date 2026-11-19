package paginate

import (
	"sync"
	"testing"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Build a test schema with the actual Buf-generated extensions. No generated
// test code or application-specific schemas are needed.
var annotatedDescriptor = sync.OnceValue(func() protoreflect.MessageDescriptor {
	mapped := func(column string) *descriptorpb.FieldOptions {
		options := &descriptorpb.FieldOptions{}
		proto.SetExtension(options, queryPB.E_Column, column)
		return options
	}
	nested := &descriptorpb.FieldOptions{}
	proto.SetExtension(nested, queryPB.E_Nested, true)
	proto.SetExtension(nested, queryPB.E_Filterable, false)
	proto.SetExtension(nested, queryPB.E_Sortable, false)
	hidden := mapped("mapped_rows.hidden")
	proto.SetExtension(hidden, queryPB.E_Filterable, false)
	proto.SetExtension(hidden, queryPB.E_Sortable, false)
	score := mapped("mapped_rows.score")
	proto.SetExtension(score, queryPB.E_AllowedFilterOperators, []queryPB.FilterOperator{
		queryPB.FilterOperator_FILTER_OPERATOR_EQUALS,
		queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN,
	})
	proto.SetExtension(score, queryPB.E_AllowedSortOperators, []queryPB.SortOperator{queryPB.SortOperator_SORT_OPERATOR_STANDARD})
	jsonField := mapped("mapped_rows.payload")
	proto.SetExtension(jsonField, queryPB.E_Filterable, true)
	proto.SetExtension(jsonField, queryPB.E_AllowedFilterOperators, []queryPB.FilterOperator{queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL})
	field := func(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type, options *descriptorpb.FieldOptions) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{Name: new(name), Number: new(number), Type: kind.Enum(), Options: options}
	}
	details := field("details", 3, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, nested)
	details.TypeName = new(".annotations.Details")
	opaque := field("opaque", 4, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, nil)
	opaque.TypeName = new(".annotations.Details")
	repeated := field("repeated_details", 5, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, nested)
	repeated.TypeName = new(".annotations.Details")
	repeated.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	cycle := field("cycle", 6, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, nested)
	cycle.TypeName = new(".annotations.Row")
	payload := field("payload", 8, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, jsonField)
	payload.TypeName = new(".annotations.Details")
	status := field("status", 3, descriptorpb.FieldDescriptorProto_TYPE_ENUM, mapped("mapped_rows.status"))
	status.TypeName = new(".annotations.State")
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: new("annotated_pagination.proto"), Package: new("annotations"), Syntax: new("proto3"),
		Dependency: []string{"otpki/pgprotopaginate/v1/query.proto"},
		EnumType: []*descriptorpb.EnumDescriptorProto{{Name: new("State"), Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: new("UNSPECIFIED"), Number: proto.Int32(0)},
			{Name: new("ACTIVE"), Number: proto.Int32(1)},
			{Name: new("INACTIVE"), Number: proto.Int32(2)},
		}}},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: new("Row"), Field: []*descriptorpb.FieldDescriptorProto{
				field("id", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64, mapped("mapped_rows.id")),
				field("name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING, mapped("mapped_rows.db_name")),
				details, opaque, repeated, cycle,
				field("hidden", 7, descriptorpb.FieldDescriptorProto_TYPE_STRING, hidden), payload,
			}},
			{Name: new("Details"), Field: []*descriptorpb.FieldDescriptorProto{
				field("score", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64, score),
				field("category", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING, mapped("c.label")), status,
			}},
		},
	}, protoregistry.GlobalFiles)
	if err != nil {
		panic(err)
	}
	return file.Messages().ByName("Row")
})

type annotatedProto struct{ message *dynamicpb.Message }

func (m *annotatedProto) ProtoReflect() protoreflect.Message {
	if m == nil {
		return dynamicpb.NewMessage(annotatedDescriptor())
	}
	if m.message == nil {
		m.message = dynamicpb.NewMessage(annotatedDescriptor())
	}
	return m.message
}

func TestAnnotatedFieldResolution(t *testing.T) {
	t.Parallel()
	fd := annotatedDescriptor()
	info, err := queryField(fd, "details.score")
	require.NoError(t, err)
	require.Equal(t, "mapped_rows.score", info.Column)
	require.Equal(t, "details.score", info.Name)
	require.True(t, info.Filterable)
	require.False(t, getFieldInfo(fd)["details"].Filterable)
	require.Equal(t, queryPB.FieldType_FIELD_TYPE_INT, getFieldInfo(fd)["details.status"].FieldType)
	for _, path := range []string{"unknown", "opaque.score", "repeated_details.score", "cycle.id", "c.label", "details.missing"} {
		_, err := queryField(fd, path)
		require.Error(t, err, path)
	}
	// The same schema name in an independent descriptor must not reuse stale cache entries.
	other := proto.CloneOf(protodesc.ToFileDescriptorProto(fd.ParentFile()))
	other.MessageType[0].Field[1].Options = &descriptorpb.FieldOptions{}
	proto.SetExtension(other.MessageType[0].Field[1].Options, queryPB.E_Column, "other.name")
	otherFile, err := protodesc.NewFile(other, protoregistry.GlobalFiles)
	require.NoError(t, err)
	otherInfo, err := queryField(otherFile.Messages().ByName("Row"), "name")
	require.NoError(t, err)
	require.Equal(t, "other.name", otherInfo.Column)
}

func TestAnnotatedFilterAndSortConversion(t *testing.T) {
	t.Parallel()
	fd := annotatedDescriptor()
	filter := &queryPB.Filter{Column: "details.score", Operator: queryPB.FilterOperator_FILTER_OPERATOR_GREATER_THAN, Values: []*queryPB.FilterValue{{IntValue: 10}}}
	params := NewPaginationParams()
	require.NoError(t, convertFilters([]*queryPB.Filter{filter, filter}, params, fd))
	require.Equal(t, int64(10), params.Gt["mapped_rows.score"])
	require.Equal(t, "mapped_rows.score", params.FilterGroups[0].Filters[0].Column)
	group := &queryPB.FilterGroup{Operator: queryPB.LogicalOperator_LOGICAL_OPERATOR_OR, Filters: []*queryPB.Filter{filter}}
	require.NoError(t, convertFilterGroups([]*queryPB.FilterGroup{group}, params, fd))
	require.Equal(t, "mapped_rows.score", params.FilterGroups[1].Filters[0].Column)
	require.Equal(t, "details.score", filter.Column, "requests must not be mutated")
	sorts := []*queryPB.SortEntry{{Column: "details.score", Order: queryPB.SortOrder_SORT_ORDER_DESC}}
	require.NoError(t, convertSortEntries(sorts, params, fd))
	require.Equal(t, "mapped_rows.score", params.Sort[0].Column)
	require.Equal(t, "details.score", sorts[0].Column)
	for _, column := range []string{"hidden", "repeated_details.score", "details.missing", "mapped_rows.score"} {
		invalid := proto.CloneOf(filter)
		invalid.Column = column
		require.Error(t, convertFilters([]*queryPB.Filter{invalid}, NewPaginationParams(), fd))
		require.Error(t, convertFilterGroups([]*queryPB.FilterGroup{{Filters: []*queryPB.Filter{invalid}}}, NewPaginationParams(), fd))
		require.Error(t, convertSortEntries([]*queryPB.SortEntry{{Column: column}}, NewPaginationParams(), fd))
	}
	invalid := proto.CloneOf(filter)
	invalid.Operator = queryPB.FilterOperator_FILTER_OPERATOR_LIKE
	require.Error(t, convertFilters([]*queryPB.Filter{invalid}, NewPaginationParams(), fd))
	require.Error(t, convertSortEntries([]*queryPB.SortEntry{{Column: "details.score", JsonText: "x"}}, NewPaginationParams(), fd))
	require.Error(t, convertSortEntries([]*queryPB.SortEntry{nil}, NewPaginationParams(), fd))
	require.NoError(t, convertFilters([]*queryPB.Filter{{Column: "payload", Operator: queryPB.FilterOperator_FILTER_OPERATOR_IS_NULL}}, NewPaginationParams(), fd))
}

func TestAnnotatedMetadataAndQueryBinding(t *testing.T) {
	t.Parallel()
	response, err := GetPaginationInfoForModel[*annotatedProto, annotatedDBRow]()
	require.NoError(t, err)
	columns := map[string]*queryPB.PaginationInfoEntry{}
	for _, column := range response.PaginationInfo.Columns {
		columns[column.Column] = column
	}
	require.True(t, columns["name"].Filterable)
	require.True(t, columns["details.score"].Sortable)
	require.False(t, columns["details.category"].Filterable, "joined field is not in base model")
	require.False(t, columns["details"].Filterable)
	require.False(t, columns["payload"].Filterable, "payload is not in the database model")
	params := NewPaginationParams()
	params.Gt["details.score"] = "12"
	request := ConvertToProtoRequestWithDescriptor(params, annotatedDescriptor())
	require.Equal(t, int64(12), request.Filters[0].Values[0].GetIntValue())
	require.Equal(t, "details.score", request.Filters[0].Column)
}

type annotatedDBRow struct {
	ID         int64  `db:"id"`
	DBName     string `db:"db_name"`
	Score      int64  `db:"score"`
	Status     int32  `db:"status"`
	CategoryID int64  `db:"category_id"`
}

func annotatedRowToProto(row *annotatedDBRow) (*annotatedProto, error) {
	result := &annotatedProto{}
	message := result.ProtoReflect()
	fields := message.Descriptor().Fields()
	message.Set(fields.ByName("id"), protoreflect.ValueOfInt64(row.ID))
	message.Set(fields.ByName("name"), protoreflect.ValueOfString(row.DBName))
	details := message.Mutable(fields.ByName("details")).Message()
	details.Set(details.Descriptor().Fields().ByName("score"), protoreflect.ValueOfInt64(row.Score))
	details.Set(details.Descriptor().Fields().ByName("status"), protoreflect.ValueOfEnum(protoreflect.EnumNumber(row.Status)))
	return result, nil
}

func annotatedID(message *annotatedProto) int64 {
	return message.ProtoReflect().Get(message.ProtoReflect().Descriptor().Fields().ByName("id")).Int()
}

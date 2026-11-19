package paginate

import (
	"math"
	"strconv"
	"testing"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestUnsignedFilterValueBoundaries(t *testing.T) {
	for _, kind := range []descriptorpb.FieldDescriptorProto_Type{
		descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
				Name:   new("unsigned_filter.proto"),
				Syntax: new("proto3"),
				MessageType: []*descriptorpb.DescriptorProto{{
					Name: new("FilterTarget"),
					Field: []*descriptorpb.FieldDescriptorProto{{
						Name: new("value"), Number: proto.Int32(1), Type: kind.Enum(),
					}},
				}},
			}, nil)
			require.NoError(t, err)
			descriptor := file.Messages().Get(0)
			for _, value := range []uint64{0, 1, math.MaxUint32, math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
				t.Run(strconv.FormatUint(value, 10), func(t *testing.T) {
					inputs := []any{value, strconv.FormatUint(value, 10)}
					if strconv.IntSize == 64 || value <= math.MaxUint32 {
						inputs = append(inputs, uint(value))
					}
					for _, input := range inputs {
						req := ConvertToProtoRequestWithDescriptor(&PaginationParams{
							Eq: map[string][]any{"value": {input}},
						}, descriptor)
						require.Len(t, req.Filters, 1)
						require.Len(t, req.Filters[0].Values, 1)
						got := req.Filters[0].Values[0]
						if value > math.MaxInt64 {
							require.Equal(t, strconv.FormatUint(value, 10), got.StringValue)
							require.Zero(t, got.IntValue)
							decoded, err := convertFilterValues([]*queryPB.FilterValue{got})
							require.NoError(t, err)
							require.Equal(t, []any{strconv.FormatUint(value, 10)}, decoded)
						} else {
							require.EqualValues(t, value, got.IntValue)
							require.Empty(t, got.StringValue)
						}
					}
				})
			}
			for _, input := range []string{"-1", "18446744073709551616", "invalid"} {
				got := convertValuesToProtobufWithType([]any{input}, descriptor.Fields().Get(0))
				require.Equal(t, input, got[0].StringValue)
				require.Zero(t, got[0].IntValue)
			}
		})
	}
}

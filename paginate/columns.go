package paginate

import (
	"slices"
	"strings"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/samber/oops"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func isNestedQueryField(field protoreflect.FieldDescriptor) bool {
	nested, _ := proto.GetExtension(field.Options(), queryPB.E_Nested).(bool)
	if !nested || field.Message() == nil || field.Cardinality() == protoreflect.Repeated {
		return false
	}
	switch field.Message().FullName() {
	case "google.protobuf.Timestamp", "google.protobuf.Duration", "google.protobuf.Any":
		return false
	default:
		return true
	}
}

func queryField(fd protoreflect.MessageDescriptor, path string) (FieldInfo, error) {
	field, ok := getFieldInfo(fd)[path]
	if !ok {
		return FieldInfo{}, oops.In(ServiceName).Errorf("field '%s' not found in message", path)
	}
	return field, nil
}

func mapFilter(filter *queryPB.Filter, field FieldInfo) *queryPB.Filter {
	result := proto.CloneOf(filter)
	result.Column = field.Column
	return result
}

// selectedColumnSource recognizes a column projected under a different name.
// Additional SELECT expressions are supplied by the server, never the client.
func selectedColumnSource(expression string) string {
	parts := strings.Fields(expression)
	if len(parts) == 3 && strings.EqualFold(parts[1], "AS") {
		return parts[0]
	}
	return strings.TrimSpace(expression)
}

func hasQueryColumn(columns []string, column string) bool {
	return slices.Contains(columns, column) || slices.Contains(columns, unqualifyColumn(column))
}

// projectedColumn is the name visible in a scanned row and outside a CTE.
func projectedColumn(params *PaginationParams, column string) string {
	for _, expression := range params.additionalSelectColumns {
		if selectedColumnSource(expression) == column {
			return extractColumnAlias(expression)
		}
	}
	return unqualifyColumn(column)
}

package paginate

import (
	"reflect"
	"strings"
	"sync"

	queryPB "buf.build/gen/go/omnitrust/pgprotopaginate/protocolbuffers/go/otpki/pgprotopaginate/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/samber/oops"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// global reflection cache
var (
	globalTypeCache  sync.Map // map[reflect.Type]*TypeInfo
	globalProtoCache sync.Map // map[string]map[string]FieldInfo
)

type TypeInfo struct {
	ColumnNames    []string              // all column names in order
	FieldMappings  map[string]int        // column name -> field index
	FieldToColumn  map[int]string        // field index -> column name
	ScanFields     []reflect.StructField // field descriptors for scanning
	ExportedFields []int                 // indices of exported fields only
	UUIDColumns    map[string]bool       // column names that are UUID types
}

type FieldInfo struct {
	Name string
	// JSONName               string
	Type                   string
	Filterable             bool
	Sortable               bool
	AllowedFilterOperators []queryPB.FilterOperator
	AllowedSortOperators   []queryPB.SortOperator
	FieldType              queryPB.FieldType
	Repeated               bool
}

// GetTypeInfo returns cached type information or builds it if not exists
func GetTypeInfo(model any) (*TypeInfo, error) {
	if model == nil {
		return nil, oops.In(ServiceName).New("model cannot be nil")
	}

	value, err := indirectModelValue(model)
	if err != nil {
		return nil, err
	}

	if value.Kind() != reflect.Struct {
		return nil, oops.In(ServiceName).New("model must be a struct or pointer to struct")
	}

	t := value.Type()

	// try to get from cache
	if cached, ok := globalTypeCache.Load(t); ok {
		info, valid := cached.(*TypeInfo)
		if !valid || info == nil {
			return nil, oops.In(ServiceName).New("invalid cached type information")
		}
		return info, nil
	}

	// cache miss
	typeInfo := buildTypeInfo(t)
	globalTypeCache.Store(t, typeInfo)

	return typeInfo, nil
}

func buildTypeInfo(t reflect.Type) *TypeInfo {
	typeInfo := &TypeInfo{
		FieldMappings: make(map[string]int),
		FieldToColumn: make(map[int]string),
		UUIDColumns:   make(map[string]bool),
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)

		// skip unexported fields for most operations
		if !field.IsExported() {
			continue
		}

		// add to exported fields list
		typeInfo.ExportedFields = append(typeInfo.ExportedFields, i)
		typeInfo.ScanFields = append(typeInfo.ScanFields, field)

		// determine column name
		columnName := ""
		if dbTag := field.Tag.Get("db"); dbTag != "" {
			columnName = dbTag
		} else if jsonTag := field.Tag.Get("json"); jsonTag != "" {
			jsonName, _, _ := strings.Cut(jsonTag, ",")
			if jsonName != "" && jsonName != "-" {
				columnName = jsonName
			}
		}

		// fall back to field name if no tags
		if columnName == "" {
			columnName = field.Name
		}

		// skip if explicitly ignored
		if columnName == "-" {
			continue
		}

		typeInfo.ColumnNames = append(typeInfo.ColumnNames, columnName)
		typeInfo.FieldMappings[columnName] = i
		typeInfo.FieldToColumn[i] = columnName

		// Check if this field is a UUID type
		if isUUIDType(field.Type) {
			typeInfo.UUIDColumns[columnName] = true
		}
	}

	return typeInfo
}

func indirectModelValue(model any) (reflect.Value, error) {
	if model == nil {
		return reflect.Value{}, oops.In(ServiceName).New("model cannot be nil")
	}

	value := reflect.ValueOf(model)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Value{}, oops.In(ServiceName).New("model cannot be nil")
		}
		value = value.Elem()
	}

	return value, nil
}

// isUUIDType checks if a field type is a UUID type
func isUUIDType(t reflect.Type) bool {
	// Handle pointer types
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Check for all known uuid types
	return (t == reflect.TypeFor[uuid.UUID]() ||
		t == reflect.TypeFor[uuid.NullUUID]() ||
		t == reflect.TypeFor[pgtype.UUID]())
}

// GetColumnNames returns cached column names for a model
func GetColumnNames(model any) ([]string, error) {
	typeInfo, err := GetTypeInfo(model)
	if err != nil {
		return nil, err
	}

	columns := make([]string, len(typeInfo.ColumnNames))
	copy(columns, typeInfo.ColumnNames)
	return columns, nil
}

// GetFieldIndex returns cached field index for a column name
func GetFieldIndex(t reflect.Type, columnName string) (int, bool) {
	// try to get from cache
	if cached, ok := globalTypeCache.Load(t); ok {
		typeInfo, valid := cached.(*TypeInfo)
		if !valid || typeInfo == nil {
			return 0, false
		}
		index, found := typeInfo.FieldMappings[columnName]
		return index, found
	}

	// cache miss
	typeInfo := buildTypeInfo(t)
	globalTypeCache.Store(t, typeInfo)

	index, found := typeInfo.FieldMappings[columnName]
	return index, found
}

// GetColumnNameForField returns cached column name for a field index
func GetColumnNameForField(t reflect.Type, fieldIndex int) (string, bool) {
	// try to get from cache
	if cached, ok := globalTypeCache.Load(t); ok {
		typeInfo, valid := cached.(*TypeInfo)
		if !valid || typeInfo == nil {
			return "", false
		}
		columnName, found := typeInfo.FieldToColumn[fieldIndex]
		return columnName, found
	}

	// cache miss
	typeInfo := buildTypeInfo(t)
	globalTypeCache.Store(t, typeInfo)

	columnName, found := typeInfo.FieldToColumn[fieldIndex]
	return columnName, found
}

// GetFieldInfo returns field information for a protobuf message
func GetFieldInfo(messageType proto.Message) map[string]FieldInfo {
	typeName := string(messageType.ProtoReflect().Descriptor().FullName())

	// try to get from cache
	if cached, ok := globalProtoCache.Load(typeName); ok {
		if fields, valid := cached.(map[string]FieldInfo); valid {
			return fields
		}
	}

	// cache miss
	fd := messageType.ProtoReflect().Descriptor()
	fields := make(map[string]FieldInfo)

	for i := 0; i < fd.Fields().Len(); i++ {
		field := fd.Fields().Get(i)
		info := FieldInfo{
			Name: string(field.Name()),
			// JSONName:   field.JSONName(),
			Type:       field.Kind().String(),
			Filterable: isFieldFilterable(field),
			Sortable:   isFieldSortable(field),
			FieldType:  getFieldType(field),
			Repeated:   field.Cardinality() == protoreflect.Repeated,
		}

		// get allowed operators
		if opts := field.Options(); opts != nil {
			if proto.HasExtension(opts, queryPB.E_AllowedSortOperators) {
				if allowedOps := proto.GetExtension(opts, queryPB.E_AllowedSortOperators); allowedOps != nil {
					if operators, ok := allowedOps.([]queryPB.SortOperator); ok {
						info.AllowedSortOperators = operators
					}
				}
			}
			if proto.HasExtension(opts, queryPB.E_AllowedFilterOperators) {
				if allowedOps := proto.GetExtension(opts, queryPB.E_AllowedFilterOperators); allowedOps != nil {
					if operators, ok := allowedOps.([]queryPB.FilterOperator); ok {
						info.AllowedFilterOperators = operators
					}
				}
			}
		}

		fields[info.Name] = info
	}

	globalProtoCache.Store(typeName, fields)
	return fields
}

// getFieldType maps protobuf field kinds to FieldType enum
func getFieldType(field protoreflect.FieldDescriptor) queryPB.FieldType {
	switch field.Kind() {
	case protoreflect.StringKind:
		return queryPB.FieldType_FIELD_TYPE_STRING
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		return queryPB.FieldType_FIELD_TYPE_INT
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return queryPB.FieldType_FIELD_TYPE_DOUBLE
	case protoreflect.BoolKind:
		return queryPB.FieldType_FIELD_TYPE_BOOL
	case protoreflect.MessageKind:
		// Check if it's a timestamp
		if field.Message().FullName() == "google.protobuf.Timestamp" {
			return queryPB.FieldType_FIELD_TYPE_TIMESTAMP
		}
		// Check if it's a duration
		if field.Message().FullName() == "google.protobuf.Duration" {
			return queryPB.FieldType_FIELD_TYPE_DURATION
		}
		return queryPB.FieldType_FIELD_TYPE_UNSPECIFIED
	case protoreflect.BytesKind:
		return queryPB.FieldType_FIELD_TYPE_BYTES
	default:
		return queryPB.FieldType_FIELD_TYPE_UNSPECIFIED
	}
}

// ExtractModelValues extracts values from a model struct for the given columns using cached type info
func ExtractModelValues(model any, columns []string) (map[string]any, error) {
	typeInfo, err := GetTypeInfo(model)
	if err != nil {
		return nil, err
	}

	value, err := indirectModelValue(model)
	if err != nil {
		return nil, err
	}

	result := make(map[string]any)

	for _, column := range columns {
		fieldIndex, found := typeInfo.FieldMappings[column]
		if !found {
			return nil, oops.In(ServiceName).Errorf("field %s not found in model", column)
		}

		fieldValue := value.Field(fieldIndex)
		result[column] = fieldValue.Interface()
	}

	return result, nil
}

// CreateModelInstance creates a new instance of the model type, handling pointer types correctly
// uses the existing type cache to avoid redundant reflection operations
func CreateModelInstance(modelType any) (any, error) {
	// get the type info from cache
	typeInfo, err := GetTypeInfo(modelType)
	if err != nil {
		return nil, err
	}

	t := reflect.TypeOf(modelType)

	// if it's a pointer type, create a new instance of the pointed-to type
	if t.Kind() == reflect.Pointer {
		// use the cached struct type from ScanFields[0].Type to create instance
		structType := typeInfo.ScanFields[0].Type
		elemValue := reflect.New(structType).Elem()
		return elemValue.Addr().Interface(), nil
	}

	// for non-pointer types, use the cached type directly
	value := reflect.New(t).Elem()
	return value.Interface(), nil
}

// CreateProtoInstance creates a new instance of the protobuf message type
func CreateProtoInstance[T proto.Message]() (T, error) {
	var zero T
	protoType := reflect.TypeOf(zero)

	if protoType.Kind() == reflect.Pointer {
		// Create a new instance of the pointed-to type
		instance := reflect.New(protoType.Elem()).Interface()
		message, ok := instance.(T)
		if !ok {
			return zero, oops.In(ServiceName).New("unexpected protobuf instance type")
		}
		return message, nil
	}

	// For non-pointer types, create a zero value
	var instance T
	return instance, nil
}

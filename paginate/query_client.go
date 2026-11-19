package paginate

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type QueryClient struct {
	params url.Values
}

func NewQueryClient() *QueryClient {
	return &QueryClient{
		params: make(url.Values),
	}
}

func (qc *QueryClient) PageSize(size int) *QueryClient {
	qc.params.Set("page_size", strconv.Itoa(size))
	return qc
}

func (qc *QueryClient) Skip(skip int) *QueryClient {
	qc.params.Set("skip", strconv.Itoa(skip))
	return qc
}

func (qc *QueryClient) PageToken(token string) *QueryClient {
	qc.params.Set("page_token", token)
	return qc
}

func (qc *QueryClient) WithTokenPagination() *QueryClient {
	qc.params.Set("with_token_pagination", "true")
	return qc
}

func (qc *QueryClient) WithCountEstimate() *QueryClient {
	qc.params.Set("with_count_estimate", "true")
	return qc
}

func (qc *QueryClient) WithoutCount() *QueryClient {
	qc.params.Set("without_count", "true")
	return qc
}

func (qc *QueryClient) WithPaginationInfoOnly() *QueryClient {
	qc.params.Set("with_pagination_info_only", "true")
	return qc
}

func (qc *QueryClient) Sort(column string) *QueryClient {
	qc.params.Add("sort", column)
	return qc
}

func (qc *QueryClient) SortAsc(column string) *QueryClient {
	qc.params.Add("sort", column)
	return qc
}

func (qc *QueryClient) SortDesc(column string) *QueryClient {
	qc.params.Add("sort", "-"+column)
	return qc
}

func (qc *QueryClient) SortField(field, value string) *QueryClient {
	qc.params.Add("sort["+field+"]", value)
	return qc
}

func (qc *QueryClient) Eq(field string, value any) *QueryClient {
	qc.params.Add("eq["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) NotEq(field string, value any) *QueryClient {
	qc.params.Add("not_eq["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) Gt(field string, value any) *QueryClient {
	qc.params.Add("gt["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) Gte(field string, value any) *QueryClient {
	qc.params.Add("gte["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) Lt(field string, value any) *QueryClient {
	qc.params.Add("lt["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) Lte(field string, value any) *QueryClient {
	qc.params.Add("lte["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) Between(field string, lowerBound, upperBound any) *QueryClient {
	qc.params.Add("between["+field+"]", formatQueryValue(lowerBound)+","+formatQueryValue(upperBound))
	return qc
}

func (qc *QueryClient) Like(field, pattern string) *QueryClient {
	qc.params.Add("like["+field+"]", url.QueryEscape(pattern))
	return qc
}

func (qc *QueryClient) ILike(field, pattern string) *QueryClient {
	qc.params.Add("ilike["+field+"]", url.QueryEscape(pattern))
	return qc
}

func (qc *QueryClient) In(field string, values ...any) *QueryClient {
	var strValues []string
	for _, v := range values {
		strValues = append(strValues, formatQueryValue(v))
	}
	qc.params.Add("in["+field+"]", strings.Join(strValues, ","))
	return qc
}

func (qc *QueryClient) NotIn(field string, values ...any) *QueryClient {
	var strValues []string
	for _, v := range values {
		strValues = append(strValues, formatQueryValue(v))
	}
	qc.params.Add("not_in["+field+"]", strings.Join(strValues, ","))
	return qc
}

func (qc *QueryClient) IsNull(field string) *QueryClient {
	qc.params.Add("is_null", field)
	return qc
}

func (qc *QueryClient) IsNotNull(field string) *QueryClient {
	qc.params.Add("is_not_null", field)
	return qc
}

func (qc *QueryClient) IsEmpty(field string) *QueryClient {
	qc.params.Add("is_empty", field)
	return qc
}

func (qc *QueryClient) IsNotEmpty(field string) *QueryClient {
	qc.params.Add("is_not_empty", field)
	return qc
}

func (qc *QueryClient) JSONContains(field, value string) *QueryClient {
	qc.params.Add("json_contains["+field+"]", url.QueryEscape(value))
	return qc
}

func (qc *QueryClient) JSONExists(field, path string) *QueryClient {
	qc.params.Add("json_exists["+field+"]", url.QueryEscape(path))
	return qc
}

func (qc *QueryClient) JSONAnyExists(field, path string) *QueryClient {
	qc.params.Add("json_any_exists["+field+"]", url.QueryEscape(path))
	return qc
}

func (qc *QueryClient) JSONAllExists(field, path string) *QueryClient {
	qc.params.Add("json_all_exists["+field+"]", url.QueryEscape(path))
	return qc
}

func (qc *QueryClient) JSONPathQuery(field, query string) *QueryClient {
	qc.params.Add("json_path_query["+field+"]", url.QueryEscape(query))
	return qc
}

func (qc *QueryClient) ArrayContains(field string, value any) *QueryClient {
	qc.params.Add("array_contains["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) ArrayContainedBy(field string, value any) *QueryClient {
	qc.params.Add("array_contained_by["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) ArrayOverlap(field string, value any) *QueryClient {
	qc.params.Add("array_overlap["+field+"]", formatQueryValue(value))
	return qc
}

func (qc *QueryClient) AddRaw(key, value string) *QueryClient {
	qc.params.Add(key, value)
	return qc
}

func (qc *QueryClient) SetRaw(key, value string) *QueryClient {
	qc.params.Set(key, value)
	return qc
}

func (qc *QueryClient) Build() string {
	return qc.params.Encode()
}

func (qc *QueryClient) String() string {
	return qc.Build()
}

// BuildWithPrefixString returns the query string with a prefix (e.g., "?")
func (qc *QueryClient) BuildWithPrefixString(prefix string) string {
	queryString := qc.params.Encode()
	if queryString == "" {
		return ""
	}
	return prefix + queryString
}

func formatQueryValue(value any) string {
	switch v := value.(type) {
	case *timestamppb.Timestamp:
		if v == nil {
			return ""
		}
		return v.AsTime().Format(time.RFC3339Nano)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case *durationpb.Duration:
		if v == nil {
			return ""
		}
		return v.AsDuration().String()
	case time.Duration:
		return v.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

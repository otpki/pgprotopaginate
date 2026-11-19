package paginate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBindQueryStringToStruct_BetweenFromQueryClient(t *testing.T) {
	t.Parallel()

	query := NewQueryClient().Between("not_before", "min-value", "max-value").Build()

	params, err := BindQueryStringToStruct(query)
	require.NoError(t, err)
	require.Contains(t, params.Between, "not_before")
	require.Equal(t, [2]any{"min-value", "max-value"}, params.Between["not_before"])
}

func TestBindQueryStringToStruct_BetweenTimestampFromQueryClient(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.March, 26, 9, 0, 0, 123000000, time.UTC)
	end := start.Add(2 * time.Hour)
	query := NewQueryClient().
		Between("not_before", timestamppb.New(start), timestamppb.New(end)).
		Build()

	params, err := BindQueryStringToStruct(query)
	require.NoError(t, err)
	require.Equal(t, [2]any{
		start.Format(time.RFC3339Nano),
		end.Format(time.RFC3339Nano),
	}, params.Between["not_before"])
}

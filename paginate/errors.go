package paginate

import "errors"

// ErrInvalidRequest identifies invalid pagination input. Use errors.Is to check
// for it; database, configuration, and result-conversion errors are not marked.
var ErrInvalidRequest = errors.New("invalid pagination request")

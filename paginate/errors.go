package paginate

import "fmt"

// ErrInvalidRequest identifies invalid pagination input. Use errors.Is to check
// for it; database, configuration, and result-conversion errors are not marked.
var ErrInvalidRequest = fmt.Errorf("invalid pagination request")

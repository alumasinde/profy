package verification

import (
	"errors"
	"io"
)

var errTooLarge = errors.New("too large")

// readAll reads at most max bytes, failing if the input is larger.
func readAll(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errTooLarge
	}
	return b, nil
}

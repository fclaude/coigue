//go:build !linux

package clipboard

import "os"

func openUnnamed() (*os.File, error) {
	return nil, errNoUnnamed
}

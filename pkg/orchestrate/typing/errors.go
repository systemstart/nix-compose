package typing

import "errors"

var (
	ErrUnknownDefinition = errors.New("unknown definition")
	ErrNotFound          = errors.New("not found")
)

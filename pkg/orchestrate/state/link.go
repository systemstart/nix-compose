package state

import (
	"fmt"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
)

// Link represents a dependency edge between two resources.
type Link struct {
	Source *typing.SimpleReference `json:"source"`
	Target *typing.SimpleReference `json:"target"`
}

func (l *Link) String() string {
	return fmt.Sprintf("link[%s -> %s]", l.Source, l.Target)
}

// NewLink creates a Link from source to target references.
func NewLink(source, target typing.Reference) *Link {
	if source.GetId() == "" {
		panic("source has no id")
	}
	return &Link{
		Source: &typing.SimpleReference{
			Id: source.GetId(), Key: source.GetKey(),
		},
		Target: &typing.SimpleReference{
			Id: target.GetId(), Key: target.GetKey(),
		},
	}
}

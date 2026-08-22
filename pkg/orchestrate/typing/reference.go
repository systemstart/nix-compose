package typing

import (
	"encoding/json"
	"fmt"
	"log"
)

// Identity is implemented by anything that has a unique ID.
type Identity interface {
	GetId() string
}

// Reference identifies a resource by ID and DefinitionKey.
type Reference interface {
	Identity
	Defined
	String() string
}

// NewReference creates a SimpleReference. Fatals if id is empty.
func NewReference(id string, key DefinitionKey) Reference {
	if id == "" {
		log.Fatalf("%s has no id", key)
	}
	return &SimpleReference{Id: id, Key: key}
}

// SimpleReference is the default Reference implementation.
type SimpleReference struct {
	Id   string          `json:"id"`
	Key  DefinitionKey   `json:"key"`
	Body json.RawMessage `json:"body"`
}

func (r *SimpleReference) GetId() string {
	return r.Id
}

func (r *SimpleReference) String() string {
	return fmt.Sprintf("[SimpleReference to %s %s]",
		r.Id, r.Key)
}

func (r *SimpleReference) GetKey() DefinitionKey {
	return r.Key
}

func (r *SimpleReference) GetBody() json.RawMessage {
	return r.Body
}

// ReferenceList is a slice of Reference with JSON and helper methods.
type ReferenceList []Reference

func (rl *ReferenceList) UnmarshalJSON(b []byte) error {
	var references []*SimpleReference
	err := json.Unmarshal(b, &references)
	if err != nil {
		return fmt.Errorf("unmarshal failed: %w", err)
	}
	result := make(ReferenceList, len(references))
	for i, r := range references {
		result[i] = r
	}

	*rl = result
	return nil
}

func (rl *ReferenceList) MarshalJSON() ([]byte, error) {
	tmp := make([]*SimpleReference, len(*rl))

	for i, r := range *rl {
		tmp[i] = &SimpleReference{Id: r.GetId(), Key: r.GetKey()}
	}

	data, err := json.Marshal(tmp)
	if err != nil {
		return nil, fmt.Errorf("marshal reference list: %w", err)
	}
	return data, nil
}

func (rl *ReferenceList) Unique() ReferenceList {
	check := make(map[string]bool)
	result := make(ReferenceList, 0, len(*rl))

	for _, r := range *rl {
		_, exists := check[r.GetId()]
		if exists {
			continue
		}
		check[r.GetId()] = true
		result = append(result, r)

	}
	return result
}

func (rl *ReferenceList) Without(toRemove Reference) ReferenceList {
	if len(*rl) == 0 {
		return *rl
	}
	result := make(ReferenceList, 0, len(*rl)-1)
	found := false
	for _, item := range *rl {
		if item == toRemove {
			found = true
			continue
		}
		result = append(result, item)
	}
	if !found {
		return *rl
	}
	return result
}

func (rl ReferenceList) First() Reference {
	if len(rl) == 0 {
		return nil
	}
	return rl[0]
}

func (rl *ReferenceList) ByKey(filter DefinitionKey) ReferenceList {
	result := make(ReferenceList, 0)

	for _, item := range *rl {
		if item.GetKey() != filter {
			continue
		}
		result = append(result, item)
	}
	return result
}
